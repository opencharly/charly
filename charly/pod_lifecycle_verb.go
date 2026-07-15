package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
)

// pod_lifecycle_verb.go — the `charly start` / `charly stop` VERBS routed through the unified
// ResolveTarget → LifecycleTarget path (the K4 deep-body move): a pod deploy reaches the plugin's
// OpStart/OpStop body (via the F6 dispatch + arbiter bracket, pod_lifecycle_dispatch.go); the former
// inline runDirect/runQuadlet/StopCmd bodies are DELETED. This mirrors `charly update`'s
// dispatchByDeployTarget (update_deploy_dispatch.go) — one verb, one substrate-agnostic dispatch, no
// per-kind code. The deploy node comes from the per-host deploy config (dc.Bundle[key]) — the SAME
// source StartCmd's arbiter claim + status read already used — with a synthesized pod node fallback
// for a deploy that has a quadlet but no dc entry (a legacy configure path; the plugin only needs
// Target=pod + the box/instance to resolve the plan).

// resolveLifecycleDeployNode resolves the deploy node for a start/stop verb from the per-host config.
func resolveLifecycleDeployNode(box, instance string) (*BundleNode, string) {
	key := deployKey(box, instance)
	if dc := deploykit.LoadDeployConfigForRead("charly start/stop"); dc != nil {
		if node, ok := dc.Bundle[key]; ok {
			n := node
			if n.Target == "" || n.Target == "container" {
				n.Target = "pod"
			}
			return &n, key
		}
	}
	return &BundleNode{Target: "pod"}, key
}

// dispatchLifecycleTarget resolves the deploy → its LifecycleTarget (connecting external substrate
// plugins first, R3 with bundle add / update), returning a clear error for a targetless substrate.
func dispatchLifecycleTarget(verb, box, instance string) (LifecycleTarget, error) {
	node, deployName := resolveLifecycleDeployNode(box, instance)
	dir, _ := os.Getwd()
	loadDeployPlugins(dir, deployName, nil)
	// A bare box with NO deploy entry (an UNCONFIGURED image `charly shell`/`cmd`/`logs` targets — the
	// former standalone-podman path) synthesizes a {Target:"pod"} node that no tree node references, so
	// the reference-scoped loadDeployPlugins never built its substrate plugin. Connect the substrate
	// deploy provider by word (idempotent — a no-op once registered; local-first, network-free) so the
	// interactive/logs legs work on an unconfigured image, not only on a configured deploy.
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

// startViaLifecycle drives `charly start` through LifecycleTarget.Start; the direct-mode CLI extras
// ride the ctx (podStartOpts) into the pod start-plan hook.
func startViaLifecycle(box, instance string, opts podStartOpts) error {
	lt, err := dispatchLifecycleTarget("start", box, instance)
	if err != nil {
		return err
	}
	return lt.Start(withPodStartOpts(context.Background(), opts))
}

// stopViaLifecycle drives `charly stop` through LifecycleTarget.Stop; --unmount rides the ctx.
func stopViaLifecycle(box, instance string, unmount bool) error {
	lt, err := dispatchLifecycleTarget("stop", box, instance)
	if err != nil {
		return err
	}
	return lt.Stop(withPodStopUnmount(context.Background(), unmount))
}
