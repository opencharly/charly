package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"
)

// host_build_check_run.go — the "check-run" F10 host-builder (P12), now serving EXACTLY ONE mode:
// "preflight" (K1-unblock wave — box/live/feature-live/score all dispatch plugin-side now, see
// candy/plugin-check/command.go's hostCheckRunCtx header). Preflight's image-ensure leg
// (dispatchBuildEnsure, charly/dispatch_build_ensure.go — core-min wave 3, build-engine cluster
// relocation) reaches the compiled-in candy/plugin-build build:ensure word; the ONE thing this
// arm still does host-side is the agent-provisioned filter (needs the loaded project's full
// bundle tree, venueIsAgentProvisioned — no sdk-portable equivalent), the same reasoning as
// hostFeatureBox below. The action noun is CLASS-GENERIC ("check-run"), never a substrate word
// (the F11 uniform-API gate).
//
// It returns RESULT DATA only (the []StepResult the plugin formats + tallies into an exit
// code); the plugin owns the CLI parse, the "Image:" header, the text/yaml/tap reporters, and
// the exit-code mapping (CheckFailExitCode).
const checkRunBuilderKind = "check-run"

// hostCheckRunPreflight is the "preflight" atom arm: for a host-target iterate entity, ensure every
// candidate image in req.Filter (the deduplicated, sorted image set the plugin's
// preflightImageCandidates already discovered from the include-EXPANDED scored plan —
// CHECK-cone move, candy/plugin-check/preflight_images.go) is present in local storage BEFORE the
// harness runner walks them. What stays host-side is exactly the ONE genuinely host-loader-coupled
// bit a plugin cannot do itself: the agent-provisioned filter (needs the loaded project's full
// bundle tree, venueIsAgentProvisioned). The image-ensure leg itself is dispatchBuildEnsure — the
// R3-shared build-engine helper, now a call into the compiled-in build:ensure plugin word rather
// than an in-core function. Registered directly as the "check-run" host-builder (single-mode kind
// — the former Mode switch collapsed once every other mode moved plugin-side).
func hostCheckRunPreflight(ctx context.Context, req spec.CheckRunRequest, _ buildEngineContext) (kit.CheckRunReply, error) {
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
	fmt.Fprintf(os.Stderr, "preflight: ensuring %d image(s) present in podman storage\n", len(req.Filter))
	for _, ref := range req.Filter {
		if loaderkit.VenueIsAgentProvisioned(uf, ref) {
			continue
		}
		if err := dispatchBuildEnsure(ctx, ref, dir, "", ""); err != nil {
			return kit.CheckRunReply{}, fmt.Errorf("preflight: %w", err)
		}
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
	registerHostBuilder(checkRunBuilderKind, typedHostBuilder(checkRunBuilderKind, hostCheckRunPreflight))
	return true
}()
