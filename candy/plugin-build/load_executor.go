package build

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"
)

// load_executor.go — the plugin-build LoaderExecutor (K3 build-engine, U6). It is the SAME six-leg
// loaderkit.LoaderExecutor the K1-LOADER witness (candy/plugin-bundle/load_executor.go) uses, one
// level up the stack: it lets candy/plugin-build drive loaderkit.LoadUnified ITSELF, plugin-side,
// so the build-engine RESOLVE runs in the plugin without importing charly core. Each
// registry-/host-coupled loader step dispatches over sdk.Executor.HostBuild to charly's "loader-*"
// host legs (charly/host_build_loader.go). Byte-identical shape to the bundle witness — the ONLY
// difference is the consuming package.
type buildLoaderExecutor struct {
	ctx context.Context
	ex  *sdk.Executor
}

// LoaderThreaded returns the CURRENT registry snapshot (∅ → spec.Threaded).
func (e *buildLoaderExecutor) LoaderThreaded() spec.Threaded {
	var t spec.Threaded
	if out, err := e.ex.HostBuild(e.ctx, "loader-threaded", nil); err == nil {
		_ = json.Unmarshal(out, &t)
	}
	return t
}

// RunBootstrapPhase runs the host's bootstrap-phase plugins over the raw root bytes ([]byte→[]byte).
func (e *buildLoaderExecutor) RunBootstrapPhase(data []byte) []byte {
	if out, err := e.ex.HostBuild(e.ctx, "loader-bootstrap", data); err == nil {
		return out
	}
	return data
}

// WalkProject runs the host's kind-blind import/discover/namespace walk.
func (e *buildLoaderExecutor) WalkProject(dir string, rootData []byte) (spec.LoadedProject, error) {
	reqJSON, err := json.Marshal(spec.LoaderWalkRequest{Dir: dir, RootData: rootData})
	if err != nil {
		return spec.LoadedProject{}, err
	}
	out, err := e.ex.HostBuild(e.ctx, "loader-walk", reqJSON)
	if err != nil {
		return spec.LoadedProject{}, err
	}
	var lp spec.LoadedProject
	if err := json.Unmarshal(out, &lp); err != nil {
		return spec.LoadedProject{}, fmt.Errorf("loader-walk: decode reply: %w", err)
	}
	return lp, nil
}

// MaterializeLoadedProject runs the host MATERIALIZE + root-wins MERGE (RULING 1 host leg).
func (e *buildLoaderExecutor) MaterializeLoadedProject(lp *spec.LoadedProject, merged *loaderkit.UnifiedFile, _ map[int64]*loaderkit.UnifiedFile) error {
	reqJSON, err := json.Marshal(lp)
	if err != nil {
		return err
	}
	out, err := e.ex.HostBuild(e.ctx, "loader-materialize", reqJSON)
	if err != nil {
		return err
	}
	if err := loaderkit.UnmarshalMaterialized(out, merged); err != nil {
		return fmt.Errorf("loader-materialize: decode reply: %w", err)
	}
	return nil
}

// ValidateAndroidDevices / ValidatePreemptible run the two registry-coupled validators host-side.
func (e *buildLoaderExecutor) ValidateAndroidDevices(uf *loaderkit.UnifiedFile) error {
	return e.validateLeg("loader-android-validate", uf)
}

func (e *buildLoaderExecutor) ValidatePreemptible(uf *loaderkit.UnifiedFile) error {
	return e.validateLeg("loader-preempt-validate", uf)
}

func (e *buildLoaderExecutor) validateLeg(kind string, uf *loaderkit.UnifiedFile) error {
	reqJSON, err := loaderkit.MarshalMaterialized(uf)
	if err != nil {
		return err
	}
	_, err = e.ex.HostBuild(e.ctx, kind, reqJSON)
	return err
}
