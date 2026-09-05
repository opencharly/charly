package main

import (
	"github.com/opencharly/spec/spec"
)

// load_executor_host.go — Unit C of the K1-LOADER RELOCATION: the COMPILED-IN, TYPED placement of
// loaderkit.LoaderExecutor. charly.LoadUnified no longer hand-builds a loaderkit.LoadSeams from its
// host functions; instead it drives loaderkit.LoadUnified through loaderkit.LoadSeamsFromExecutor
// over this hostLoaderExecutor — the SAME seam constructor a genuine out-of-module PLUGIN uses
// (candy/plugin-fleet's execLoaderExecutor, the Unit D witness), but reaching each registry-/
// host-coupled step by calling the host function DIRECTLY (zero marshal, U3). The typed
// LoaderExecutor interface is what makes that free: a compiled-in placement pays no envelope tax,
// exactly like the ProjectWalker / Materializer typed seams (spec/loader_seam.go).
//
// FLOOR (#118 P15): this is the HOST'S OWN loader-entry seam — the permanent typed LoaderExecutor the
// floored LoadUnified entry (loader_threaded.go, since config.go/unified.go were deleted) drives loaderkit through so the HOST can load its own
// charly.yml (a plugin host must read its own config to bootstrap; that never leaves core). It is the
// host half of the loader-seam M-mechanism, the same class as the floored host_build_loader_floor.go
// permanent legs. The loaderkit
// import is the SAME shared P16b import-purity concern that load entry carries, tracked separately;
// it does not make this file transitional (the earlier "deleted at GREEN" framing predated the
// config/unified floor decision that keeps the host's own loader entry permanent).
type hostLoaderExecutor struct{}

// LoaderThreaded returns the CURRENT registry-derived snapshot — called FRESH at each DATA-seam
// invocation (never cached), because the walk's connect-declared-kind pass mutates the registry
// between seam construction and the post-walk validators.
func (hostLoaderExecutor) LoaderThreaded() spec.Threaded { return loaderThreaded() }

// InKindConnectPass reports whether the current load is running inside the walk's
// connect-declared-kind pre-pass (connectDeclaredKindPlugins → LoadConfig, inKindConnectPass=true).
// It satisfies the sdk's OPTIONAL connectPassAwareExecutor capability: loaderkit's
// LoadSeamsFromExecutor bypasses the materialized-tree cache for the connect pass's transient,
// deferred-entity materialization (R1 — the external-kind decode regression: caching the nested
// load's deferred tree under the outer load's key handed the outer load a tree with the kind
// entities missing). An out-of-process plugin executor has no connect pass and does not implement
// it — the cache stays active for plugin loads.
func (hostLoaderExecutor) InKindConnectPass() bool { return inKindConnectPass() }

// RunBootstrapPhase invokes every registered bootstrap-phase plugin on the raw root bytes. The
// direct in-process call has no failure mode of its own (runBootstrapPhase already tolerates every
// per-provider error internally) — the error return exists to match the LoaderExecutor interface's
// out-of-process sibling (executorLoaderExecutor), which DOES have a real IPC failure mode.
func (hostLoaderExecutor) RunBootstrapPhase(data []byte) ([]byte, error) {
	return runBootstrapPhase(data), nil
}

// WalkProject runs the kind-blind import/discover/namespace walk (the registered spec.ProjectWalker,
// reached via the host's spec.WalkSeams) → the generic spec.LoadedProject envelope.
func (hostLoaderExecutor) WalkProject(dir string, rootData []byte) (spec.LoadedProject, error) {
	return hostWalkProject(dir, rootData)
}

// MaterializeLoadedProject replays the per-document/per-namespace MATERIALIZE + root-wins MERGE over
// the walk envelope. The kind-blind orchestration lives in loaderkit (#48), reached through the
// compiled-in plugin-loader spec.ProjectLoader seam (#55 2b C3 — so charly core never imports
// loaderkit); this leg supplies ONLY the host's three coupled leaf legs (hostMaterializeProjectSeams
// — registry kind-decode, discovered-manifest fold, embedded defaults).
func (hostLoaderExecutor) MaterializeLoadedProject(lp *spec.LoadedProject, merged *spec.UnifiedFile, byID map[int64]*spec.UnifiedFile) error {
	return requireProjectLoader().MaterializeLoadedProject(lp, merged, byID, hostMaterializeProjectSeams())
}

// ValidateAndroidDevices enforces the kind:android box⊻adb XOR. The VALIDATION LOGIC lives in
// loaderkit (reached through the plugin-loader seam); this leg supplies ONLY the host registry-resolve
// callback (resolveAndroidViaPlugin) — the genuine host coupling.
func (hostLoaderExecutor) ValidateAndroidDevices(uf *spec.UnifiedFile) error {
	return requireProjectLoader().ValidateAndroidDevices(uf, resolveAndroidViaPlugin)
}

// ValidatePreemptible validates preemptible / requires_exclusive / requires_shared across the deploy
// map, including the resource-vocabulary cross-check. The VALIDATION LOGIC lives in loaderkit (reached
// through the plugin-loader seam); this leg supplies ONLY the host registry-resolve callbacks
// (resolveResourceViaPlugin / resolveVmViaPlugin) — the genuine host coupling.
func (hostLoaderExecutor) ValidatePreemptible(uf *spec.UnifiedFile) error {
	return requireProjectLoader().ValidatePreemptible(uf, resolveResourceViaPlugin, resolveVmViaPlugin)
}
