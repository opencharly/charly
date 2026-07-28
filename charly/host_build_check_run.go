package main

import (
	"context"
	"fmt"
	"os"

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
// bundle tree, venueIsAgentProvisioned — no sdk-portable equivalent). The action noun is
// CLASS-GENERIC ("check-run"), never a substrate word (the F11 uniform-API gate). (The former
// build-scope hostFeatureBox engine relocated to candy/plugin-check's pluginCheckRunFeatureBox in
// cone-C #31 — see the relocation note below.)
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

// hostFeatureBox (the build-scope `charly box feature run` engine) RELOCATED to candy/plugin-check
// (feature_box_gather.go's pluginCheckRunFeatureBox, reached via Mode:"feature-box") in cone-C #31 —
// `charly box feature run` is now candy/plugin-box's command:feature, bridging to that plugin engine
// over InvokeProvider. Its former in-core CLI leaf (check_feature_run.go's BoxFeatureRunCmd) is
// DELETED with the move.

// Register the check-run host-builder at package-var init (before any init(), like the
// config-resolve / cli / vm-build builders).
var _ = func() bool {
	registerHostBuilder(checkRunBuilderKind, typedHostBuilder(checkRunBuilderKind, hostCheckRunPreflight))
	return true
}()
