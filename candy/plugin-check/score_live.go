package check

// score_live.go — K1-unblock wave, arm 3 (the "score" check-run mode): pluginCheckRunScore, the
// plugin-resident port of charly/host_build_check_run.go's hostCheckRunScore +
// charly/check_runner_live.go's RunCheckLive/scoreOnePodBucket/resolveScoringChain — the AI
// harness's end-of-iteration scorer, walking the SUBSTITUTED scoring plan (nonce-carrying,
// req.Plan) against the live deployments its check:/agent-check: steps target via each step's
// loader-derived Op.Venue. Unlike "box"/"live"/"feature-live" (reached via CheckCmd leaves calling
// hostCheckRun), "score" is reached from harness_loop.go's scoreLive — a DIFFERENT call site that
// needs its OWN per-call ctx (a watchdog probe's bounded context), not the package-level cmdCtx —
// so command.go's dispatch splits into hostCheckRun (cmdCtx) / hostCheckRunCtx (explicit ctx),
// both routing through the SAME Mode switch (R3).
//
// Every dependency was already portable: scoredSteps/topoSortScored/groupScoredByPod/
// bucketSteps/skippedStepScore/isEphemeralDeploy/ephemeralKeepOnFailure (live_scoring.go, Unit A),
// isScored/scoredPlanOrigin (score.go, Unit A), deploykit.ResolveDeployChain/
// RootExecutorForDeployNode/ContainerChain, resolveHostVarsForSteps (members.go), and
// newPluginCheckRunner (plugin_runner.go) for the per-bucket runner. deployRoots comes off the
// resolved-project envelope (derefDeployTree(rp.Deploy)) instead of resolveTreeRoot(cwd) — the
// SAME project-level tree Unit A/B already established as the envelope's Deploy projection.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// pluginCheckRunScore is the "score" mode: walk the substituted scoring plan (req.Plan) against
// the live deployments its steps target, returning the per-step verdicts in reply.Score. The port
// of charly/host_build_check_run.go's hostCheckRunScore.
func pluginCheckRunScore(ex *sdk.Executor, ctx context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	results, err := pluginRunCheckLive(ex, ctx, req.Name, req.Plan)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	return kit.CheckRunReply{Score: results}, nil
}

// pluginRunCheckLive scores `plan` against the live containers its check:/agent-check: steps
// target via Op.Venue — the port of charly/check_runner_live.go's RunCheckLive.
func pluginRunCheckLive(ex *sdk.Executor, ctx context.Context, scoreName string, plan []spec.Step) (*spec.CheckRunResults, error) {
	if len(plan) == 0 {
		return &spec.CheckRunResults{}, nil
	}

	entries := scoredSteps(plan)

	// Defensive venue check on scored steps (the validator catches this earlier).
	for _, e := range entries {
		if isScored(e.step) && e.step.Venue == "" {
			return nil, fmt.Errorf("scored step %q has empty venue (no tree position resolved) — refusing to score", e.id)
		}
	}

	// Topologically order by depends_on, then group consecutive same-venue runs.
	sorted, cyclic := topoSortScored(entries)

	out := &spec.CheckRunResults{Box: "score:" + scoreName, Mode: "run"}
	verdictByID := make(map[string]string, len(entries))

	dir, _ := os.Getwd()
	var deployRoots map[string]spec.BundleNode
	// Best-effort, matching the core original's own resolveTreeRoot(cwd) tolerance (a missing/
	// absent project never fails scoring — a plan whose steps address a bare venue by container
	// name still scores via pluginResolveScoringChain's roots==nil fallback). A nil ex (unit
	// tests; no reverse channel) skips the envelope fetch entirely rather than dereferencing nil.
	if ex != nil {
		if rp, rerr := resolvedProject(ex, ctx, dir); rerr == nil && rp != nil {
			deployRoots = derefDeployTree(rp.Deploy)
		}
	}

	for _, bucket := range groupScoredByPod(sorted) {
		if len(bucket) == 0 {
			continue
		}
		pluginScoreOneVenueBucket(ex, ctx, dir, bucket, deployRoots, out, verdictByID)
	}

	// Cyclic scored steps get a deterministic fail verdict.
	for _, e := range cyclic {
		if !isScored(e.step) {
			continue
		}
		out.Step = append(out.Step, spec.StepScore{
			ID:            e.id,
			Origin:        "pod:" + e.step.Venue,
			Text:          e.step.KeywordText(),
			Tag:           kit.EffectiveTags(e.step.Tag),
			Status:        "fail",
			SkippedReason: "cycle: step is part of a depends_on cycle",
		})
		out.Summary.Total++
		out.Summary.Fail++
		verdictByID[e.id] = "fail"
	}
	return out, nil
}

// pluginScoreOneVenueBucket scores one same-venue bucket of (topologically ordered) scored steps:
// it optionally ephemeral-wraps the venue (deploy add / del), resolves and reachability-probes the
// scoring executor chain, builds the bucket's runner, then runs each step — appending verdicts to
// out and recording them in verdictByID. The port of charly/check_runner_live.go's
// scoreOnePodBucket.
func pluginScoreOneVenueBucket(ex *sdk.Executor, ctx context.Context, dir string, bucket []scoredStep, deployRoots map[string]spec.BundleNode, out *spec.CheckRunResults, verdictByID map[string]string) {
	venue := bucket[0].step.Venue

	var ephemeralCleanup func(bool)
	if venue != "" && isEphemeralDeploy(deployRoots, venue) {
		fmt.Fprintf(os.Stderr, "score live: ephemeral wrap — charly bundle add %s\n", venue)
		exe, _ := os.Executable()
		addCmd := exec.Command(exe, "bundle", "add", venue)
		addCmd.Stderr = os.Stderr
		addCmd.Stdout = os.Stdout
		if err := addCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "score live: ephemeral add %s failed: %v\n", venue, err)
		}
		keepOnFailure := ephemeralKeepOnFailure(deployRoots, venue)
		ephemeralCleanup = func(failed bool) {
			if failed && keepOnFailure {
				fmt.Fprintf(os.Stderr, "score live: keep_on_failure=true; leaving %s alive\n", venue)
				return
			}
			delCmd := exec.Command(exe, "bundle", "del", venue, "--assume-yes")
			delCmd.Stderr = os.Stderr
			delCmd.Stdout = os.Stdout
			_ = delCmd.Run()
		}
	}

	chainExec, chainErr := pluginResolveScoringChain(deployRoots, venue)
	reachableErr := chainErr
	if reachableErr == nil {
		o, _, exit, err := chainExec.RunCapture(ctx, "echo ok")
		if err != nil {
			reachableErr = fmt.Errorf("chain %q unreachable: %w", chainExec.Venue(), err)
		} else if exit != 0 {
			reachableErr = fmt.Errorf("chain %q probe non-zero (%d): %s", chainExec.Venue(), exit, strings.TrimSpace(o))
		}
	}
	if reachableErr != nil {
		fmt.Fprintf(os.Stderr, "score live: venue %q unreachable: %v\n", venue, reachableErr)
	}

	var runner *kit.Runner
	var hostCleanups []func()
	if reachableErr == nil {
		roots := deployRoots
		hostVars, cleanups := resolveHostVarsForSteps(ex, ctx, dir, bucketSteps(bucket), "")
		hostCleanups = cleanups
		runner = newPluginCheckRunner(ex, ctx, spec.CheckEnv{
			Mode:      "run",
			Box:       venue,
			VenueKind: chainExec.Kind(),
		}, kit.RunnerConfig{
			Exec:     chainExec,
			Mode:     kit.ModeLive,
			Box:      venue,
			HostVars: hostVars,
			TargetResolver: kit.VenueResolver(func(v string) (kit.Executor, map[string]string, bool, error) {
				vex, err := pluginResolveScoringChain(roots, v)
				if err != nil {
					return nil, nil, false, err
				}
				return vex, map[string]string{}, false, nil
			}),
		})
	}

	for _, e := range bucket {
		// depends_on cascade — only matters for scored steps.
		if isScored(e.step) {
			if blocked := kit.FirstUnmetDepStep(e.step, verdictByID); blocked != "" {
				out.Step = append(out.Step, skippedStepScore(e, venue, blocked))
				out.Summary.Total++
				out.Summary.Skip++
				verdictByID[e.id] = "skipped"
				continue
			}
		}

		if reachableErr != nil {
			if isScored(e.step) {
				out.Step = append(out.Step, spec.StepScore{
					ID:     e.id,
					Origin: "pod:" + venue,
					Text:   e.step.KeywordText(),
					Tag:    kit.EffectiveTags(e.step.Tag),
					Status: "fail",
				})
				out.Summary.Total++
				out.Summary.Fail++
				verdictByID[e.id] = "fail"
			}
			continue
		}

		// Run the single step via RunPlan against the bucket's runner.
		set := &kit.LabelDescriptionSet{Candy: []kit.LabeledDescription{{
			Origin: "pod:" + venue,
			Plan:   []spec.Step{e.step},
		}}}
		results := kit.RunPlan(ctx, runner, set, false)
		if !isScored(e.step) {
			continue // provisioning run: step — executed, not scored
		}
		status := "fail"
		if len(results) > 0 {
			status = results[0].Result.Status.String()
		}
		score := spec.StepScore{
			ID:      e.id,
			Origin:  "pod:" + venue,
			Text:    e.step.KeywordText(),
			Tag:     kit.EffectiveTags(e.step.Tag),
			Keyword: string(kit.KeywordOf(&e.step)),
			Status:  status,
		}
		if len(results) > 0 {
			score.Verb = results[0].Result.Verb
		}
		out.Step = append(out.Step, score)
		out.Summary.Total++
		switch status {
		case "pass":
			out.Summary.Pass++
			verdictByID[e.id] = "pass"
		case "fail":
			out.Summary.Fail++
			verdictByID[e.id] = "fail"
		default: // skip
			out.Summary.Skip++
			verdictByID[e.id] = "fail"
		}
	}

	if ephemeralCleanup != nil {
		bucketFailed := false
		for _, e := range bucket {
			if v, ok := verdictByID[e.id]; ok && v == "fail" {
				bucketFailed = true
				break
			}
		}
		ephemeralCleanup(bucketFailed)
	}
	kit.CloseHostCleanups(hostCleanups)
}

// pluginResolveScoringChain returns the DeployExecutor chain that reaches `venue` — the port of
// charly/check_runner_live.go's resolveScoringChain, off the envelope-derived deployRoots instead
// of a fresh resolveTreeRoot(cwd) call.
func pluginResolveScoringChain(roots map[string]spec.BundleNode, venue string) (deploykit.DeployExecutor, error) {
	if strings.Contains(venue, ".") && roots != nil {
		_, chain, err := deploykit.ResolveDeployChain(roots, venue, kit.ShellExecutor{})
		if err == nil {
			return chain, nil
		}
		return nil, fmt.Errorf("step venue %q is dotted but does not resolve through the deploy tree: %w", venue, err)
	}
	if roots != nil {
		if node, ok := roots[venue]; ok && node.Descent != nil && node.Descent.HostRooted {
			return deploykit.RootExecutorForDeployNode(&node)
		}
	}
	return deploykit.ContainerChain("podman", "charly-"+venue), nil
}
