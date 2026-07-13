package main

// check_runner_live.go — score a plan's check:/agent-check: steps against the
// live deployments they target via the step's loader-derived Op.Venue field.
//
// Each scored step carries an Op.Venue (stamped from its bundle-tree position
// by flattenBundleVenues) naming the container its probe runs against. The
// scorer groups steps by venue, runs each group against
// `charly-<venue>` (or a dotted deploy chain), and classifies pass/fail. run:
// steps in the plan provision (executed in order) but are not scored;
// check:/agent-check: steps are the scored success criteria.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// scoredStep pairs a plan step with its stable declaration-order id so the ids
// stay consistent across live scoring regardless of the topo/bucket execution reorder.
type scoredStep struct {
	id   string
	step Step
}

// scoredPlanOrigin is the fixed origin used to derive step ids so that
// scoredSteps and RunCheckLive produce matching ids.
const scoredPlanOrigin = "plan"

// scoredSteps wraps a plan with stable ids by declaration order.
func scoredSteps(plan []Step) []scoredStep {
	out := make([]scoredStep, len(plan))
	for i := range plan {
		out[i] = scoredStep{id: EffectiveStepID(&plan[i], scoredPlanOrigin, i), step: plan[i]}
	}
	return out
}

// isScored reports whether a step is a scored success criterion (check: or
// agent-check:).
func isScored(s Step) bool { return s.Check != "" || s.AgentCheck != "" }

// RunCheckLive scores `plan` against the live containers its check:/agent-check:
// steps target via Op.Pod. Returns a spec.CheckRunResults shaped like
// ParseCharlyTestOutput's so the scorer (Classify, fingerprints, summary)
// consumes it unchanged. `deployment` is legacy/unused; `scoreName` labels the
// run.
func RunCheckLive(ctx context.Context, deployment, scoreName string, plan []Step) (*spec.CheckRunResults, error) {
	_ = deployment

	if len(plan) == 0 {
		return &spec.CheckRunResults{}, nil
	}

	entries := scoredSteps(plan)

	// Defensive pod check on scored steps (validator catches earlier).
	for _, e := range entries {
		if isScored(e.step) && e.step.Venue == "" {
			return nil, fmt.Errorf("scored step %q has empty venue (no tree position resolved) — refusing to score", e.id)
		}
	}

	// Topologically order by depends_on, then group consecutive same-pod runs.
	sorted, cyclic := topoSortScored(entries)

	out := &spec.CheckRunResults{Box: "score:" + scoreName, Mode: "run"}
	verdictByID := make(map[string]string, len(entries))

	cwd, _ := os.Getwd()
	deployRoots, _ := resolveTreeRoot(cwd)

	for _, bucket := range groupScoredByPod(sorted) {
		if len(bucket) == 0 {
			continue
		}
		scoreOnePodBucket(ctx, bucket, deployRoots, out, verdictByID)
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
			Tag:           EffectiveTags(e.step.Tag),
			Status:        "fail",
			SkippedReason: "cycle: step is part of a depends_on cycle",
		})
		out.Summary.Total++
		out.Summary.Fail++
		verdictByID[e.id] = "fail"
	}
	return out, nil
}

// scoreOnePodBucket scores one same-pod bucket of (topologically ordered)
// scored steps: it optionally ephemeral-wraps the pod (deploy add / del),
// resolves and reachability-probes the scoring executor chain, builds the
// bucket's runner, then runs each step — appending verdicts to out and
// recording them in verdictByID. Split out of RunCheckLive, which keeps the
// outer pod-grouping loop.
func scoreOnePodBucket(ctx context.Context, bucket []scoredStep, deployRoots map[string]BundleNode, out *spec.CheckRunResults, verdictByID map[string]string) {
	pod := bucket[0].step.Venue

	var ephemeralCleanup func(bool)
	if pod != "" && isEphemeralDeploy(deployRoots, pod) {
		fmt.Fprintf(os.Stderr, "score live: ephemeral wrap — charly bundle add %s\n", pod)
		exe, _ := os.Executable()
		addCmd := exec.Command(exe, "bundle", "add", pod)
		addCmd.Stderr = os.Stderr
		addCmd.Stdout = os.Stdout
		if err := addCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "score live: ephemeral add %s failed: %v\n", pod, err)
		}
		keepOnFailure := ephemeralKeepOnFailure(deployRoots, pod)
		ephemeralCleanup = func(failed bool) {
			if failed && keepOnFailure {
				fmt.Fprintf(os.Stderr, "score live: keep_on_failure=true; leaving %s alive\n", pod)
				return
			}
			delCmd := exec.Command(exe, deployDelArgv(pod)...)
			delCmd.Stderr = os.Stderr
			delCmd.Stdout = os.Stdout
			_ = delCmd.Run()
		}
	}

	chainExec, chainErr := resolveScoringChain(deployRoots, pod)
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
		fmt.Fprintf(os.Stderr, "score live: pod %q unreachable: %v\n", pod, reachableErr)
	}

	var runner *kit.Runner
	var hostCleanups []func()
	if reachableErr == nil {
		roots := deployRoots
		hostVars, cleanups := resolveHostVarsForSteps(bucketSteps(bucket), "")
		hostCleanups = cleanups
		runner = newCheckRunner(kit.RunnerConfig{
			Exec:     chainExec,
			Mode:     RunModeLive,
			Box:      pod,
			HostVars: hostVars,
			TargetResolver: kit.VenueResolver(func(venue string) (kit.Executor, map[string]string, bool, error) {
				ex, err := resolveScoringChain(roots, venue)
				if err != nil {
					return nil, nil, false, err
				}
				return ex, map[string]string{}, false, nil
			}),
		})
	}

	for _, e := range bucket {
		// depends_on cascade — only matters for scored steps.
		if isScored(e.step) {
			if blocked := firstUnmetDepStep(e.step, verdictByID); blocked != "" {
				out.Step = append(out.Step, skippedStepScore(e, pod, blocked))
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
					Origin: "pod:" + pod,
					Text:   e.step.KeywordText(),
					Tag:    EffectiveTags(e.step.Tag),
					Status: "fail",
				})
				out.Summary.Total++
				out.Summary.Fail++
				verdictByID[e.id] = "fail"
			}
			continue
		}

		// Run the single step via RunPlan against the bucket's runner.
		set := &LabelDescriptionSet{Candy: []LabeledDescription{{
			Origin: "pod:" + pod,
			Plan:   []Step{e.step},
		}}}
		results := RunPlan(ctx, runner, set, nil, false)
		if !isScored(e.step) {
			continue // provisioning run: step — executed, not scored
		}
		status := "fail"
		if len(results) > 0 {
			status = results[0].Result.Status.String()
		}
		score := spec.StepScore{
			ID:      e.id,
			Origin:  "pod:" + pod,
			Text:    e.step.KeywordText(),
			Tag:     EffectiveTags(e.step.Tag),
			Keyword: string(keywordOf(&e.step)),
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
	closeHostCleanups(hostCleanups)
}

// topoSortScored orders scored steps by depends_on (id-keyed), returning the
// ordered slice and, on cycle, the non-cyclic remainder + the cyclic entries.
func topoSortScored(entries []scoredStep) (ordered, cyclic []scoredStep) {
	idToIdx := make(map[string]int, len(entries))
	for i, e := range entries {
		idToIdx[e.id] = i
	}
	indeg := make([]int, len(entries))
	fwd := make([][]int, len(entries))
	for i, e := range entries {
		for _, dep := range e.step.DependsOn {
			if d, ok := idToIdx[dep]; ok {
				fwd[d] = append(fwd[d], i)
				indeg[i]++
			}
		}
	}
	var ready []int
	for i, n := range indeg {
		if n == 0 {
			ready = append(ready, i)
		}
	}
	sortIntsAsc(ready)
	for len(ready) > 0 {
		head := ready[0]
		ready = ready[1:]
		ordered = append(ordered, entries[head])
		for _, succ := range fwd[head] {
			indeg[succ]--
			if indeg[succ] == 0 {
				ready = append(ready, succ)
				sortIntsAsc(ready)
			}
		}
	}
	if len(ordered) != len(entries) {
		fmt.Fprintf(os.Stderr, "score live: dependency cycle detected — scoring non-cyclic steps; cyclic steps reported as fail verdicts\n")
		for i, n := range indeg {
			if n > 0 {
				cyclic = append(cyclic, entries[i])
			}
		}
	}
	return ordered, cyclic
}

func sortIntsAsc(s []int) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// groupScoredByPod splits sorted scored steps into maximal same-pod runs.
func groupScoredByPod(sorted []scoredStep) [][]scoredStep {
	if len(sorted) == 0 {
		return nil
	}
	var buckets [][]scoredStep
	cur := []scoredStep{sorted[0]}
	curPod := sorted[0].step.Venue
	for _, e := range sorted[1:] {
		if e.step.Venue == curPod {
			cur = append(cur, e)
			continue
		}
		buckets = append(buckets, cur)
		cur = []scoredStep{e}
		curPod = e.step.Venue
	}
	buckets = append(buckets, cur)
	return buckets
}

func bucketSteps(b []scoredStep) []Step {
	out := make([]Step, len(b))
	for i, e := range b {
		out[i] = e.step
	}
	return out
}

// skippedStepScore builds a depends-on-cascade skip result for one scored step.
func skippedStepScore(e scoredStep, pod, blockedBy string) spec.StepScore {
	return spec.StepScore{
		ID:            e.id,
		Origin:        "pod:" + pod,
		Text:          e.step.KeywordText(),
		Tag:           EffectiveTags(e.step.Tag),
		Status:        "skipped",
		SkippedReason: "dep-unmet: " + blockedBy,
	}
}

// resolveScoringChain returns the DeployExecutor chain that reaches `pod`.
func resolveScoringChain(roots map[string]BundleNode, pod string) (DeployExecutor, error) {
	if strings.Contains(pod, ".") && roots != nil {
		_, chain, err := ResolveDeployChain(roots, pod, ShellExecutor{})
		if err == nil {
			return chain, nil
		}
		return nil, fmt.Errorf("step pod %q is dotted but does not resolve through deploy tree: %w", pod, err)
	}
	if roots != nil {
		if node, ok := roots[pod]; ok && node.Descent != nil && node.Descent.HostRooted {
			return rootExecutorForDeployNode(&node)
		}
	}
	return ContainerChain("podman", "charly-"+pod), nil
}

// RenderPlanYAML returns the plan rendered as a YAML block for ${PLAN}.
func RenderPlanYAML(plan []Step) string {
	if len(plan) == 0 {
		return ""
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(plan); err != nil {
		return fmt.Sprintf("# error rendering plan: %v", err)
	}
	_ = enc.Close()
	return buf.String()
}

// isEphemeralDeploy reports whether the named pod resolves to a charly.yml
// entry marked ephemeral.
func isEphemeralDeploy(roots map[string]BundleNode, pod string) bool {
	if pod == "" {
		return false
	}
	if node, ok := roots[pod]; ok {
		return node.IsEphemeral()
	}
	if node, _, err := ResolveNodePath(roots, pod); err == nil && node != nil {
		return node.IsEphemeral()
	}
	return false
}

// ephemeralKeepOnFailure returns the keep_on_failure flag from the named
// ephemeral deploy's lifetime block.
func ephemeralKeepOnFailure(roots map[string]BundleNode, pod string) bool {
	if pod == "" {
		return false
	}
	resolve := func(node *BundleNode) bool {
		if node == nil || node.Ephemeral == nil {
			return false
		}
		return node.Ephemeral.KeepOnFailure
	}
	if node, ok := roots[pod]; ok {
		return resolve(&node)
	}
	if node, _, err := ResolveNodePath(roots, pod); err == nil {
		return resolve(node)
	}
	return false
}
