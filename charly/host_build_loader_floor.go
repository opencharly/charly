package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// host_build_loader_floor.go — the PERMANENT loader reverse legs (K1-LOADER RELOCATION, Unit B).
// Split from the transitional legs (host_build_loader.go) so the P16 gate stays HONEST: these three
// legs dispatch to a permanent in-core M/D mechanism a plugin-side loader will ALWAYS call back for,
// so they PERSIST at #118 GREEN — classifying them as residue would make residue→0 unreachable (you
// cannot "remove" a permanent kernel mechanism). Each is the reverse-channel M-dispatch face of an
// existing floor file (bootstrap_phase.go, the prescan/connect plugin-loading machinery,
// loader_threaded.go's D-snapshot). A genuine out-of-module plugin (execLoaderExecutor, Unit D)
// reaches them over sdk.Executor.HostBuild; the compiled-in TYPED placement (hostLoaderExecutor,
// Unit C) skips the marshal (U3).
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

// Register the PERMANENT loader legs at package-var init (before any init()).
var _ = func() bool {
	registerHostBuilder("loader-bootstrap", hostBuildLoaderBootstrap)
	registerHostBuilder("loader-walk", hostBuildLoaderWalk)
	registerHostBuilder("loader-threaded", hostBuildLoaderThreaded)
	return true
}()
