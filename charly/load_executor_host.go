package main

import (
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"
)

// load_executor_host.go — Unit C of the K1-LOADER RELOCATION: the COMPILED-IN, TYPED placement of
// loaderkit.LoaderExecutor. charly.LoadUnified no longer hand-builds a loaderkit.LoadSeams from its
// host functions; instead it drives loaderkit.LoadUnified through loaderkit.LoadSeamsFromExecutor
// over this hostLoaderExecutor — the SAME seam constructor a genuine out-of-module PLUGIN uses
// (candy/plugin-bundle's execLoaderExecutor, the Unit D witness), but reaching each registry-/
// host-coupled step by calling the host function DIRECTLY (zero marshal, U3). The typed
// LoaderExecutor interface is what makes that free: a compiled-in placement pays no envelope tax,
// exactly like the ProjectWalker / Materializer typed seams (spec/loader_seam.go). This wrapper is
// TRANSITIONAL — deleted by #118 GREEN when the loader's call sites move into their owning plugin
// (no permanent charly→loaderkit wrapper; IMPORT-PURITY end-state).
type hostLoaderExecutor struct{}

// LoaderThreaded returns the CURRENT registry-derived snapshot — called FRESH at each DATA-seam
// invocation (never cached), because the walk's connect-declared-kind pass mutates the registry
// between seam construction and the post-walk validators.
func (hostLoaderExecutor) LoaderThreaded() spec.Threaded { return loaderThreaded() }

// RunBootstrapPhase invokes every registered bootstrap-phase plugin on the raw root bytes.
func (hostLoaderExecutor) RunBootstrapPhase(data []byte) []byte { return runBootstrapPhase(data) }

// WalkProject runs the kind-blind import/discover/namespace walk (the registered spec.ProjectWalker,
// reached via the host's spec.WalkSeams) → the generic spec.LoadedProject envelope.
func (hostLoaderExecutor) WalkProject(dir string, rootData []byte) (spec.LoadedProject, error) {
	return hostWalkProject(dir, rootData)
}

// MaterializeLoadedProject replays the host's per-document/per-namespace MATERIALIZE + root-wins
// MERGE over the walk envelope (registry kind-decode via the registered spec.Materializer). RULING
// 1: a TRANSITIONAL host leg — the whole embed+parser+registry orchestration stays host-side until
// task #48 relocates it into loaderkit.
func (hostLoaderExecutor) MaterializeLoadedProject(lp *spec.LoadedProject, merged *loaderkit.UnifiedFile, byID map[int64]*loaderkit.UnifiedFile) error {
	return materializeLoadedProject(lp, merged, byID)
}

// ValidateAndroidDevices enforces the kind:android box⊻adb XOR (resolves android templates via the
// plugin-substrate provider) — host-coupled, so a leg.
func (hostLoaderExecutor) ValidateAndroidDevices(uf *loaderkit.UnifiedFile) error {
	return validateAndroidDevices(uf)
}

// ValidatePreemptible validates preemptible / requires_exclusive / requires_shared across the
// deploy map, including the resource-vocabulary cross-check (resolves the resource plugin kind +
// vm/resource entities via the registry) — host-coupled, so a leg.
func (hostLoaderExecutor) ValidatePreemptible(uf *loaderkit.UnifiedFile) error {
	return validatePreemptibleUnified(uf)
}
