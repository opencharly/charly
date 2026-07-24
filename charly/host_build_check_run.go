package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// host_build_check_run.go — the generic "check-run" F10 host-builder (P12). command:check
// (candy/plugin-check) owns the `charly check` CLI + output formatting, but RUNNING a plan
// against a venue is a composite of core host-serving Mechanisms a plugin (a separate module
// importing only sdk) cannot perform: the venue→executor construction, the OCI-label plan
// extraction (ExtractMetadata), and the plan-walk's verb dispatch through the provider registry
// (the in-core VerbResolver). So the plugin resolves its intent into a spec.CheckRunRequest and
// the host builds the venue + runs the kit-Runner + returns the per-step results — exactly as
// command:vm forwards `vm build` to HostBuild("vm-build"). The action noun is CLASS-GENERIC
// ("check-run"), never a substrate word (the F11 uniform-API gate).
//
// It returns RESULT DATA only (the []StepResult the plugin formats + tallies into an exit
// code); the plugin owns the CLI parse, the "Image:" header, the text/yaml/tap reporters, and
// the exit-code mapping (CheckFailExitCode). The engine (Runner / plan walk / registry dispatch)
// stays a host-serving atom, consumed here.
const checkRunBuilderKind = "check-run"

func hostBuildCheckRun(ctx context.Context, req spec.CheckRunRequest, _ buildEngineContext) (kit.CheckRunReply, error) {
	switch req.Mode {
	case "score":
		return hostCheckRunScore(ctx, req)
	case "preflight":
		return hostCheckRunPreflight(ctx, req)
	default:
		return kit.CheckRunReply{}, fmt.Errorf("check-run: unknown mode %q", req.Mode)
	}
}

// hostCheckRunScore is the "score" atom arm (P12 Wave-2 AI harness): it walks the SUBSTITUTED
// scoring plan carried in req.Plan (nonce-carrying, NOT the OCI-baked plan the "live" mode
// extracts) against the live deployments its check:/agent-check: steps target via each step's
// loader-derived Op.Venue, returning the per-step verdicts in reply.Score. RunCheckLive stays a
// host-serving atom (its topo/pod-bucket/ephemeral-wrap build is registry/venue-coupled); the
// plugin harness owns the scoring math (Classify, fingerprints) over the returned *CheckRunResults.
func hostCheckRunScore(ctx context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	results, err := RunCheckLive(ctx, req.Name, req.Name, req.Plan)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	return kit.CheckRunReply{Score: results}, nil
}

// hostCheckRunPreflight is the "preflight" atom arm: for a host-target iterate entity, ensure every
// image the score's plan steps spawn is present in local storage BEFORE the harness runner walks
// them. The include-EXPANDED scored plan is computed PLUGIN-SIDE off the resolved-project envelope
// (candy/plugin-check's include-splicer, which owns the plan expansion) and threaded here as
// req.Plan; the host runs the R3-shared EnsureImagePresent (via ensureScoreImages) over it and
// returns an empty reply.
func hostCheckRunPreflight(_ context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	dir := req.Dir
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if !ok || uf == nil {
		return kit.CheckRunReply{}, fmt.Errorf("check-run preflight: no charly.yml in %s", dir)
	}
	if _, has := uf.Bundle[req.Name]; !has {
		return kit.CheckRunReply{}, fmt.Errorf("check-run preflight: no entity %q in %s", req.Name, dir)
	}
	if err := ensureScoreImages(context.Background(), req.Plan, uf, dir); err != nil {
		return kit.CheckRunReply{}, err
	}
	return kit.CheckRunReply{}, nil
}

// hostFeatureBox is the CLI-free engine of BoxFeatureRunCmd.Run (charly/check_feature_run.go): run
// the image's baked plan against a disposable container, deterministic steps only
// (SkipDeterministicRun skips the build-time install run: steps). `charly box feature run` stays a
// still-core CLI leaf (box.go's Kong grammar, box-command-word collision with candy/plugin-feature —
// see check_feature_run.go's header for why) and calls this DIRECTLY as an in-process Go function,
// never through the check-run HostBuild seam — Mode:"feature-box" has NO other caller (traced
// during K1-unblock wave arm 2: `charly check feature run` is deploy-scope only, Mode:"feature-live",
// wired plugin-side; a plugin-side "feature-box" twin would be unreachable dead code and was
// deliberately not built — see candy/plugin-check/feature_run_gather.go's header), so this stays
// the ONE live implementation, unmoved.
func hostFeatureBox(req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	imageRef, err := kit.ResolveLocalImageRef(rt.RunEngine, req.Image)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	meta, err := deploykit.ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		return kit.CheckRunReply{Image: imageRef, NoSteps: true}, nil
	}
	// validateTagExpr still VALIDATES --tag's syntax (a malformed expression errors here);
	// applying the parsed filter to the plan walk is a known, tracked gap — see the
	// P12a cutover notes on kit.RunPlan's tag-filter no-op (RCA'd, non-blocking, routed
	// to the next check-correctness thematic batch) — kit.RunPlan takes no filter param.
	if err := kit.ValidateTagExpr(req.Tag); err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("parsing --tag: %w", err)
	}
	// R44 Option A: ONE persistent container + `podman exec` per step.
	executor, teardown, err := deploykit.CheckBoxContainerChain(rt.RunEngine, imageRef)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	defer teardown()
	env, hasRuntime := resolverEnv(kit.ResolveCheckVarsBuild(meta))
	runner := newCheckRunner(kit.RunnerConfig{
		Exec:                 executor,
		Mode:                 RunModeBox,
		Env:                  env,
		HasRuntime:           hasRuntime,
		Distros:              meta.Distro,
		SkipDeterministicRun: true,
	})
	results := kit.RunPlan(context.Background(), runner, meta.Description, req.Strict)
	return kit.CheckRunReply{Image: imageRef, Steps: results, Header: fmt.Sprintf("Feature run (image, build scope): %s", imageRef)}, nil
}

// Register the check-run host-builder at package-var init (before any init(), like the
// config-resolve / cli / vm-build builders).
var _ = func() bool {
	registerHostBuilder(checkRunBuilderKind, typedHostBuilder(checkRunBuilderKind, hostBuildCheckRun))
	return true
}()
