package main

import (
	"fmt"
	"os"

	"github.com/opencharly/spec/spec"
)

// pod_lifecycle_verb.go — dispatchLifecycleTarget, the shared core (M) Mechanism behind every pod
// lifecycle VERB (`charly start`/`stop`/`shell`/`logs`/`cmd`/`service`, the K4 deep-body move): a
// pod deploy reaches the plugin's OpStart/OpStop/… body (via the F6 dispatch + arbiter bracket,
// pod_lifecycle_dispatch.go); the former inline runDirect/runQuadlet/StopCmd bodies are DELETED.
// This mirrors `charly update`'s dispatchByDeployTarget (update_deploy_dispatch.go) — one verb, one
// substrate-agnostic dispatch, no per-kind code. #55 W3 A10 folded the former thin
// startViaLifecycle/stopViaLifecycle wrappers (each had exactly ONE caller) directly into
// host_build_pod_lifecycle_dispatch.go's shared dispatchAndRunLifecycle helper — this file now
// keeps only the mechanism dispatchLifecycleTarget itself, which every one of the six lifecycle-
// target-shaped HostBuild handlers (start/stop/shell/logs/service/cmd) calls through that helper.
//
// #55 K4 seam-completion: the per-host deploy NODE arrives as DATA (spec.Deploy). The command:pod /
// command:cmd plugin ALREADY resolves it plugin-side (loaderkit.ResolveLifecycleDeployNodeViaExecutor,
// the cycle-free plugin-side overlay read — the dc.Bundle[key] lookup + the container/""→pod Target
// normalization + the {Target:pod} fallback for a bare image with no deploy entry) and threads it into
// the pod-lifecycle HostBuild requests. So the host no longer re-reads the per-host config here (the
// former resolveLifecycleDeployNode + its deploykit.LoadDeployConfigForRead are deleted): the config
// READ is a plugin loading capability, not a host M. What STAYS is EXACTLY the one step that cannot
// cross the plugin boundary — connect + ResolveTarget + the live-executor composition, a core (M)
// Mechanism per the kernel/plugin boundary law.

// dispatchLifecycleTarget resolves the plugin-supplied deploy node → its LifecycleTarget (connecting
// external substrate plugins first, R3 with bundle add / update), returning a clear error for a
// targetless substrate. The node is DATA the calling command plugin already resolved; the host does
// ONLY the irreducible core-M dispatch.
func dispatchLifecycleTarget(verb string, node *spec.BundleNode, deployName string) (LifecycleTarget, error) {
	if node == nil {
		return nil, fmt.Errorf("charly %s %s: no deploy node supplied by the command plugin", verb, deployName)
	}
	dir, _ := os.Getwd()
	if err := loadDeployPlugins(dir, deployName, nil); err != nil {
		return nil, err
	}
	// A bare box with NO deploy entry (an UNCONFIGURED image `charly shell`/`cmd`/`logs` targets — the
	// former standalone-podman path) is a {Target:"pod"} node the plugin synthesized that no tree node
	// references, so the reference-scoped loadDeployPlugins never built its substrate plugin. Connect the
	// substrate deploy provider by word (idempotent — a no-op once registered; local-first, network-free)
	// so the interactive/logs legs work on an unconfigured image, not only on a configured deploy.
	if node.Target != "" {
		connectPluginByWord(ClassDeployTarget, node.Target)
	}
	target, err := ResolveTarget(node, deployName)
	if err != nil {
		return nil, err
	}
	lt, ok := target.(LifecycleTarget)
	if !ok {
		return nil, fmt.Errorf("charly %s %s: %q target has no live runtime", verb, deployName, node.Target)
	}
	return lt, nil
}
