package bundle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// load_executor.go — the K1-LOADER RELOCATION WITNESS (Unit D): command:bundle drives
// loaderkit.LoadUnified ITSELF, plugin-side, proving a genuine out-of-module plugin can run the
// whole project loader without importing charly core. execLoaderExecutor implements
// loaderkit.LoaderExecutor by dispatching each registry-coupled loader step over
// sdk.Executor.HostBuild to charly's "loader-*" host legs (charly/host_build_loader_floor.go); paired
// with loaderkit.LoadSeamsFromExecutor, LoadUnified's kind-blind orchestration + the PURE relocated
// LOAD-half seams (flatten/fold/validate-members + the DATA-driven stamp / ephemeral / check-bed
// validators) run entirely plugin-side. Only the FOUR host-coupled legs (bootstrap / walk / threaded /
// materialize) cross the wire; the two capability validators (ValidateAndroidDevices /
// ValidatePreemptible) now self-serve plugin-side over InvokeProvider(kind, OpResolve). The compiled-in
// host (charly.LoadUnified) reaches the SAME host funcs directly through the TYPED hostLoaderExecutor.
type execLoaderExecutor struct {
	ctx context.Context
	ex  *sdk.Executor
}

// LoaderThreaded returns the CURRENT registry snapshot (∅ → spec.Threaded). No error return in the
// interface: in-proc this leg never fails, so a (never-observed) HostBuild error degrades to the
// zero snapshot, matching the host's own empty-registry semantics.
func (e *execLoaderExecutor) LoaderThreaded() spec.Threaded {
	var t spec.Threaded
	if out, err := e.ex.HostBuild(e.ctx, "loader-threaded", nil); err == nil {
		_ = json.Unmarshal(out, &t)
	}
	return t
}

// RunBootstrapPhase runs the host's bootstrap-phase plugins over the raw root bytes ([]byte→[]byte).
// A leg failure returns the bytes unchanged (the no-op-seam contract).
func (e *execLoaderExecutor) RunBootstrapPhase(data []byte) []byte {
	if out, err := e.ex.HostBuild(e.ctx, "loader-bootstrap", data); err == nil {
		return out
	}
	return data
}

// WalkProject runs the host's kind-blind import/discover/namespace walk (spec.LoaderWalkRequest →
// spec.LoadedProject).
func (e *execLoaderExecutor) WalkProject(dir string, rootData []byte) (spec.LoadedProject, error) {
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

// MaterializeLoadedProject runs the host MATERIALIZE + root-wins MERGE (spec.LoadedProject → the
// merged spec.UnifiedFile). The host leg owns its OWN byID scratch (RULING 1); the plugin
// passes none and decodes the reply into merged.
func (e *execLoaderExecutor) MaterializeLoadedProject(lp *spec.LoadedProject, merged *spec.UnifiedFile, _ map[int64]*spec.UnifiedFile) error {
	reqJSON, err := json.Marshal(lp)
	if err != nil {
		return err
	}
	out, err := e.ex.HostBuild(e.ctx, "loader-materialize", reqJSON)
	if err != nil {
		return err
	}
	// UnmarshalMaterialized (NOT json.Unmarshal): the reply carries UnifiedFile.PluginKinds (json:"-")
	// out-of-band, so the reconstructed merged is byte-identical to host-side — the standalone-template
	// / plugin-kind entities the downstream check-bed / android / preempt validators read survive.
	if err := loaderkit.UnmarshalMaterialized(out, merged); err != nil {
		return fmt.Errorf("loader-materialize: decode reply: %w", err)
	}
	return nil
}

// ValidateAndroidDevices runs the kind:android box⊻adb XOR validator PLUGIN-SIDE:
// loaderkit.ValidateAndroidDevices in-plugin, threading the android resolve callback over
// InvokeProvider(kind, "local", OpResolve) — no host round-trip (the loader-android-validate host
// leg dissolved).
func (e *execLoaderExecutor) ValidateAndroidDevices(uf *spec.UnifiedFile) error {
	return loaderkit.ValidateAndroidDevices(uf, loaderkit.ResolveAndroidViaExecutor(e.ctx, e.ex))
}

// ValidatePreemptible runs the preemptible/requires_exclusive/requires_shared validator PLUGIN-SIDE:
// loaderkit.ValidatePreemptible in-plugin, threading the resource/vm resolve callbacks over
// InvokeProvider(kind, OpResolve) — no host round-trip (the loader-preempt-validate host leg dissolved).
func (e *execLoaderExecutor) ValidatePreemptible(uf *spec.UnifiedFile) error {
	return loaderkit.ValidatePreemptible(uf, loaderkit.ResolveResourceViaExecutor(e.ctx, e.ex), loaderkit.ResolveVmViaExecutor(e.ctx, e.ex))
}

// resolveTreeViaLoader is the witness entry: it drives loaderkit.LoadUnified PLUGIN-SIDE (over
// execLoaderExecutor) to resolve the `charly bundle add` deploy tree, replacing the former host
// resolveTreeRoot seam. The host "deploy-plugins-connect" preamble connects the deployment's
// out-of-tree plugin candies (registry-coupled) and hands back the project dir; the project load +
// local-overlay merge (deploykit.LoadBundleConfig / MergeDeployConfigs) are pure sdk. It returns the
// merged tree and whether the root's stamped descent is the "ssh" venue (a vm root → node-only
// dispatch) — read directly from node.Descent, which loaderkit.LoadUnified stamps (byte-identical to
// the host's former registry-backed nodeTraits check for a stamped node).
func resolveTreeViaLoader(path string, addCandy []string) (map[string]spec.BundleNode, bool, string, error) {
	if cmdExec == nil {
		return nil, false, "", fmt.Errorf("bundle deploy-plugins-connect: no host reverse channel (command not compiled-in?)")
	}
	var pre spec.DeployPluginsConnectReply
	if err := hostDeploySeamJSON("deploy-plugins-connect", spec.DeployPluginsConnectRequest{
		Path:     path,
		AddCandy: addCandy,
	}, &pre); err != nil {
		return nil, false, "", err
	}

	exec := &execLoaderExecutor{ctx: cmdCtx, ex: cmdExec}
	var projectDC *deploykit.BundleConfig
	if uf, ok, err := loaderkit.LoadUnified(pre.Dir, loaderkit.LoadSeamsFromExecutor(exec)); err != nil {
		return nil, false, "", err
	} else if ok && uf != nil {
		projectDC = deploykit.ProjectBundleConfig(uf)
	}

	localDC, _ := deploykit.LoadBundleConfig()
	merged := deploykit.MergeDeployConfigs(projectDC, localDC)
	if merged == nil || merged.Bundle == nil {
		return nil, false, pre.Dir, nil
	}

	rootVenueSSH := false
	if node, _, e := deploykit.ResolveNodePath(merged.Bundle, path); e == nil && node != nil && node.Descent != nil {
		rootVenueSSH = node.Descent.Venue == "ssh"
	}
	return merged.Bundle, rootVenueSSH, pre.Dir, nil
}

// fetchExternalSubstrates returns the loader-threaded ExternalDeploySubstrates DATA snapshot (the
// EXACT set of substrate words for which the host's isExternalDeploySubstrate returns true — K1-
// LOADER RELOCATION). compileNodePlans consults it (by word, never a per-kind branch) to decide
// which targets are TARGET-ONLY (no primary image plan): `target == "local" || external[target]`,
// byte-exact to the former host compileNodePlans condition. A HostBuild failure degrades to an empty
// set (matching the host's empty-registry semantics — a bare pod deploy still compiles its image).
func fetchExternalSubstrates() map[string]bool {
	if cmdExec == nil {
		return nil
	}
	out, err := cmdExec.HostBuild(cmdCtx, "loader-threaded", nil)
	if err != nil {
		return nil
	}
	var t spec.Threaded
	if err := json.Unmarshal(out, &t); err != nil {
		return nil
	}
	return t.ExternalDeploySubstrates
}
