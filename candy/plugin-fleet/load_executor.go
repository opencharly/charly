package fleet

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// load_executor.go — the K1-LOADER RELOCATION WITNESS (Unit D): command:fleet drives the whole
// project loader ITSELF, plugin-side, proving a genuine out-of-module plugin can run it without
// importing charly core. The executor-backed loaderkit.LoaderExecutor + the LoadUnified call it
// used to carry inline (a private copy shared with three other candies) now live in the shared
// loaderkit.LoadUnifiedViaExecutor helper (#55 Cone A Unit 2, R3) — it dispatches each
// registry-coupled loader step over sdk.Executor.HostBuild to charly's "loader-*" host legs, runs
// the PURE relocated LOAD-half seams plugin-side, and self-serves the android/preemptible capability
// validators over InvokeProvider(kind, OpResolve). resolveTreeViaLoader below just calls that helper.

// resolveTreeViaLoader is the witness entry: it drives loaderkit.LoadUnifiedViaExecutor PLUGIN-SIDE
// to resolve the `charly fleet add` deploy tree, replacing the former host-resident merged-tree read. The
// host "deploy-plugins-connect" preamble connects the deployment's out-of-tree plugin candies
// (registry-coupled) and hands back the project dir; the project load + local-overlay merge
// (deploykit.LoadFleetConfig / MergeDeployConfigs) are pure sdk. It returns the merged tree and
// whether the root's stamped descent is the "ssh" venue (a vm root → node-only dispatch) — read
// directly from node.Descent, which the loader stamps (byte-identical to the host's former
// registry-backed nodeTraits check for a stamped node).
func resolveTreeViaLoader(path string, addCandy []string) (map[string]spec.FleetNode, bool, string, error) {
	if cmdExec == nil {
		return nil, false, "", fmt.Errorf("fleet deploy-plugins-connect: no host reverse channel (command not compiled-in?)")
	}
	var pre spec.DeployPluginsConnectReply
	if err := hostDeploySeamJSON("deploy-plugins-connect", spec.DeployPluginsConnectRequest{
		Path:     path,
		AddCandy: addCandy,
	}, &pre); err != nil {
		return nil, false, "", err
	}

	var projectDC *deploykit.FleetConfig
	if uf, ok, err := loaderkit.LoadUnifiedViaExecutor(cmdCtx, cmdExec, pre.Dir); err != nil {
		return nil, false, "", err
	} else if ok && uf != nil {
		projectDC = deploykit.ProjectFleetConfig(uf)
	}

	localDC, _ := deploykit.LoadFleetConfig()
	merged := deploykit.MergeDeployConfigs(projectDC, localDC)
	if merged == nil || merged.Fleet == nil {
		return nil, false, pre.Dir, nil
	}

	rootVenueSSH := false
	if node, _, e := deploykit.ResolveNodePath(merged.Fleet, path); e == nil && node != nil && node.Descent != nil {
		rootVenueSSH = node.Descent.Venue == "ssh"
	}
	return merged.Fleet, rootVenueSSH, pre.Dir, nil
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

// fetchLoaderPrimaries returns the loader-threaded Primaries DATA snapshot (plugin-verb WORD →
// scalar-sugar primary field — the SAME registry-derived map candy/plugin-build's resolve fills
// spec.ResolvedProject.Primaries from, and the host's deleted deploy-config-save leg fed
// deploykit.MarshalFleetNode via loaderThreaded().Primaries). The node-form deploy-state WRITE
// (saveDeployConfig / persistDeployState) reads it here so command:fleet resugars each plan step
// PLUGIN-SIDE — no host round-trip through the deleted deploy-config-save seam (#55 K4). A
// HostBuild failure degrades to an empty map (a plan with no plugin-verb sugar marshals identically).
func fetchLoaderPrimaries() map[string]string {
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
	return t.Primaries
}
