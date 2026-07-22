package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// builder_preresolve.go — the host-side CONNECT half of the builder deploy-time pre-pass
// (FLOOR-SLIM-proper Unit-8). The actual per-(candy,builder) OpCollectContext/OpReverse RPCs — the
// half that populates HostContext.BuilderContext — moved to candy/plugin-bundle's OWN
// preresolveBuilderContexts (compile.go's compileDeployPlans calls it, via exec.InvokeProvider,
// spike-proven live), since command:bundle already holds a live *sdk.Executor for its own
// OpCompile Invoke and already re-hydrates the resolved-project envelope those RPCs need. What
// STAYS here is genuinely host-only: loadProjectPlugins/ScanAllCandyWithConfigOpts are core-private
// project-loading mechanics no plugin can reach, so charly-core must still ensure the exact
// externalized builder plugin(s) a deploy's resolved closure triggers are build-connected BEFORE
// dispatching the compile to the plugin (InvokeProvider's own lazy-connect fallback, S2, is a
// safety net — not the primary connect path, for the same reason this pre-pass always scoped its
// connect precisely rather than relying on a blanket "all four builder plugins" load).
//
// CONNECTION IS PRECISELY SCOPED + ON-DEMAND. detects exactly the builders the deploy's RESOLVED
// closure triggers — applying the SAME distro/build-format gate the generator's deploykit
// CandyNeedsBuilder applies, so a fedora deploy never connects aur (even when a multi-distro candy
// carries an aur: section for its arch variant), and a pixi-only deploy connects ONLY pixi. It then
// build-connects just those plugins by their canonical ref (the same on-demand, scoped pattern as
// connectPluginByWordRef, R3) — NOT a blanket "all four builder plugins" surfaced across an entire
// box scan. A pure pod deploy (no add_candy) never reaches BuildDeployPlan, so it connects nothing.

// ensureBuildersConnectedForOrder detects the externalized builders order's candies need (the SAME
// deploykit.DetectExternalizedBuilders call the deleted host-side preresolveBuilderContexts made)
// and build-connects them — so by the time the OpCompile Invoke reaches candy/plugin-bundle, its
// own exec.InvokeProvider calls resolve against an already-connected provider (never depending
// solely on InvokeProvider's S2 lazy-connect fallback).
func ensureBuildersConnectedForOrder(ctx context.Context, cfg *Config, dir string, order []string, layers map[string]spec.CandyReader, img *buildkit.ResolvedBox) error {
	needed := deploykit.DetectExternalizedBuilders(order, layers, externalizedBuilders, img)
	if len(needed) == 0 {
		return nil
	}
	return ensureBuildersConnected(ctx, cfg, dir, needed)
}

// ensureBuildersConnected build-connects ONLY the not-yet-connected externalized builder plugins in
// `words`, scoped to those words — the same on-demand, scoped pattern as connectPluginByWordRef
// (R3). It scans the project's OWN candy closure first (main repo: candy/plugin-builder-<word> is a
// local candy/ dir — network-free), falling back to pulling in the SPECIFIC builder plugins by
// their canonical ref (a box/<distro> submodule deploy that triggers a builder but vendors the
// plugin nowhere; under CHARLY_REPO_OVERRIDE the ref resolves to the local superproject). A builder
// whose plugin still will not connect is a LOUD error (R4).
func ensureBuildersConnected(ctx context.Context, cfg *Config, dir string, words []string) error {
	refs := map[string]struct{}{}
	var extraRefs []string
	for _, w := range words {
		if _, ok := providerRegistry.ResolveBuilder(w); ok {
			continue // already connected this process
		}
		refs[w] = struct{}{}
		if ref, ok := externalBuilderPluginRef(w); ok {
			extraRefs = append(extraRefs, ref)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	if cfg == nil {
		return fmt.Errorf("builder plugin connect: no project config (cannot scan %v)", words)
	}
	for _, opts := range []ResolveOpts{{}, {ExtraCandyRefs: extraRefs}} {
		candyMap, scanErr := ScanAllCandyWithConfigOpts(dir, cfg, opts)
		if scanErr != nil || candyMap == nil {
			continue
		}
		if perr := loadProjectPlugins(ctx, candyMap, refs); perr != nil {
			return fmt.Errorf("builder plugin load %v: %w", words, perr)
		}
		allConnected := true
		for w := range refs {
			if _, ok := providerRegistry.ResolveBuilder(w); !ok {
				allConnected = false
				break
			}
		}
		if allConnected {
			return nil
		}
	}
	var missing []string
	for w := range refs {
		if _, ok := providerRegistry.ResolveBuilder(w); !ok {
			missing = append(missing, w)
		}
	}
	return fmt.Errorf("externalized builder plugin(s) %v could not be connected (plugin candy not found / build failed)", missing)
}
