package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"
)

// host_build_loader_floor.go — the PERMANENT loader reverse legs (K1-LOADER RELOCATION, Unit B).
// These four legs dispatch to a permanent in-core M/D mechanism a plugin-side loader will ALWAYS call
// back for, so they PERSIST at #118 GREEN — classifying them as residue would make residue→0
// unreachable (you cannot "remove" a permanent kernel mechanism). Each is the reverse-channel
// M-dispatch face of an existing floor file (bootstrap_phase.go, the prescan/connect plugin-loading
// machinery, loader_threaded.go's D-snapshot, the provider_kind_invoke.go registry kind-DECODE). A
// genuine out-of-module plugin (execLoaderExecutor, Unit D) reaches them over sdk.Executor.HostBuild;
// the compiled-in TYPED placement (hostLoaderExecutor, Unit C) skips the marshal (U3).
//
//   - loader-bootstrap → runBootstrapPhase: bootstrap-phase PLUGIN DISPATCH (M — plugin loading /
//                        phase dispatch; bootstrap_phase.go is floor). A plugin-side loader must
//                        always ask the host to invoke the registered PhaseBootstrap providers.
//   - loader-walk      → hostWalkProject: the registry-coupled walk BOUNDARY — prescanDeclaredPlugin
//                        Words + connectDeclaredKindPlugins (M — plugin loading / prescan-dispatch)
//                        + the registered ProjectWalker seam. A plugin-side loader must always call
//                        back to prescan + connect declared kind plugins before the parse.
//   - loader-threaded  → loaderThreaded: the registry-derived kind-recognition Threaded snapshot
//                        (D — kind-recognition DATA the host fills from the registry;
//                        loader_threaded.go is floor). A plugin-side loader must always pull it.
//   - loader-materialize → drives loaderkit.MaterializeLoadedProject over the host leaf seams
//                        (hostMaterializeProjectSeams: the provider_kind_invoke.go registry kind-DECODE
//                        M + discovered-manifest fold + embedded defaults). A plugin-side loader
//                        cannot resolve kinds itself — it must ALWAYS call back for the host registry
//                        kind-decode — so this leg PERSISTS (the kind-blind orchestration lives in
//                        loaderkit, #48; this is only its permanent registry-coupled leaf dispatch).

// hostBuildLoaderBootstrap runs the bootstrap-phase plugins over the raw root bytes ([]byte→[]byte).
func hostBuildLoaderBootstrap(_ context.Context, specJSON []byte, _ buildEngineContext) ([]byte, error) {
	return runBootstrapPhase(specJSON), nil
}

// hostBuildLoaderWalk runs the kind-blind import/discover/namespace walk with its registry-coupled
// boundary side-effects (spec.LoaderWalkRequest → spec.LoadedProject).
func hostBuildLoaderWalk(_ context.Context, specJSON []byte, _ buildEngineContext) ([]byte, error) {
	var req spec.LoaderWalkRequest
	if err := json.Unmarshal(specJSON, &req); err != nil {
		return nil, fmt.Errorf("loader-walk host-build: decode request: %w", err)
	}
	lp, err := hostWalkProject(req.Dir, req.RootData)
	if err != nil {
		return nil, err
	}
	return marshalJSON(lp)
}

// hostBuildLoaderThreaded returns the CURRENT registry-derived snapshot (∅ → spec.Threaded). Called
// FRESH by each DATA-seam closure, so it reflects the post-walk connect-declared-kind registry.
func hostBuildLoaderThreaded(_ context.Context, _ []byte, _ buildEngineContext) ([]byte, error) {
	return marshalJSON(loaderThreaded())
}

// hostBuildLoaderMaterialize replays the host MATERIALIZE + root-wins MERGE over the walk envelope
// (spec.LoadedProject → the merged loaderkit.UnifiedFile). The host leg OWNS its own byID scratch map
// (RULING 1) — the plugin passes none; it unmarshals the reply into its own `merged`. The kind-blind
// orchestration lives in loaderkit (#48); this compiled-in leg drives it over the host's registry-
// coupled leaf seams (hostMaterializeProjectSeams: the provider_kind_invoke.go kind-DECODE M,
// discovered-manifest fold, embedded defaults) — the genuine host coupling a plugin cannot run itself.
func hostBuildLoaderMaterialize(_ context.Context, specJSON []byte, _ buildEngineContext) ([]byte, error) {
	var lp spec.LoadedProject
	if err := json.Unmarshal(specJSON, &lp); err != nil {
		return nil, fmt.Errorf("loader-materialize host-build: decode request: %w", err)
	}
	merged := &loaderkit.UnifiedFile{}
	if err := loaderkit.MaterializeLoadedProject(&lp, merged, map[int64]*loaderkit.UnifiedFile{}, hostMaterializeProjectSeams()); err != nil {
		return nil, err
	}
	// MarshalMaterialized (NOT marshalJSON): UnifiedFile.PluginKinds is json:"-", so a plain marshal
	// would DROP every standalone-template / plugin-kind entity plugin-side (the R10 bed regression).
	return loaderkit.MarshalMaterialized(merged)
}

// Register the PERMANENT loader legs at package-var init (before any init()).
var _ = func() bool {
	registerHostBuilder("loader-bootstrap", hostBuildLoaderBootstrap)
	registerHostBuilder("loader-walk", hostBuildLoaderWalk)
	registerHostBuilder("loader-threaded", hostBuildLoaderThreaded)
	registerHostBuilder("loader-materialize", hostBuildLoaderMaterialize)
	return true
}()
