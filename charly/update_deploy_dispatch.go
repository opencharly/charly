package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/spec/spec"
)

// Deploy-name resolution + per-target dispatch for `charly update`.
//
// `charly update <name>` resolves a deploy name (VM/local/pod targets all dispatch from here) or
// a bare image name; this file consolidates the per-target dispatch into podUpdateCmd (the
// host-side reconstruction of the former UpdateCmd — now command:update in candy/plugin-pod) so
// the user-facing surface is just one verb.
//
// K-wave 2 cone CONTESTED THINned this file 178 → 104 (ledger row satisfied): the deploy-node
// resolution + the disposability transparency note moved PLUGIN-SIDE to candy/plugin-pod
// (resolveUpdateDeployNode + noteUpdateDisposability there now — the plugin resolves the merged
// tree via loaderkit.ResolveMergedTreeViaExecutor, looks up the full deploy key, prints the
// note, and threads the resolved node on the shared #PodLifecycleRequest.node envelope; the
// former #PodUpdatePayload.tree_json whole-tree thread is DELETED). What stays here is the
// irreducible core-private dispatch body dispatchByDeployTarget, which CALLS two M-mechanisms a
// plugin cannot reach:
//   - loadDeployPlugins (plugin_loader.go) IS the plugin-loading mechanism — it mutates the
//     core-private provider registry by connecting NEW out-of-process plugins. A plugin cannot
//     load another plugin into the host's own registry; that registry is core-private by
//     definition (the K1 loader keystone).
//   - ResolveTarget (unified_targets.go) reads providerRegistry.ResolveDeploy(node.Target) and
//     type-asserts the result to the core-private *grpcProvider — already explicitly named STAY
//     by the A9 orchestrator adjudication.
//
// This is the EXACT same "one step that cannot cross the plugin boundary" pattern
// pod_lifecycle_verb.go's dispatchLifecycleTarget already established for start/stop/shell/logs/
// service/cmd.
//
// Critical semantic: NONE of the dispatchers below regenerate the user-overlay deploy entry (no
// `charly fleet add` / `charly config` calls allowed in the pod path). The user's directive:
// "Any config changes should be done via charly config only." This verb updates ARTIFACTS
// (image bits, VM disk, local candies, quadlet/marker image refs); `charly config` updates
// CONFIG. The two responsibilities are strictly separated.

// dispatchByDeployTarget invokes the target-specific update helper for the deploy node the
// command:update plugin resolved PLUGIN-SIDE and threaded in (c.Node). Errors explicitly when:
//
//   - the name didn't resolve to a deploy entry (resolveUpdateDeployNode, now plugin-side, has
//     already reported "no deploy named %q" before dispatch; an absent node here is the same
//     signal)
//   - target is unknown / unsupported (kubernetes)
//
// No silent fallbacks. The user gets a clear error pointing at the right alternative or the
// field they need to fix.
func (c *podUpdateCmd) dispatchByDeployTarget() error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	node := c.Node
	if node == nil {
		return fmt.Errorf("no charly.yml found relative to %s; charly update requires a deploy name. To refresh an image artifact only, use 'charly box pull %s'", dir, c.Box)
	}

	// Normalize legacy target spellings before resolution. Empty / "container" both mean "pod"
	// (the schema invariant requires target:, so empty is only pre-migration defensiveness).
	if node.Target == "" || node.Target == "container" {
		node.Target = "pod"
	}
	deployName := c.Box

	// Connect the deployment's OUT-OF-TREE plugins before ResolveTarget, so an external deploy
	// SUBSTRATE (the E3-deploy plugin-side deploy target) resolves its grpcProvider for the
	// rebuild — the SAME loadDeployPlugins fleet add / fleet del use (R3).
	if err := loadDeployPlugins(dir, deployName, nil); err != nil {
		return err
	}

	// UNIFIED dispatch — charly update for EVERY kind routes through the SAME ResolveTarget →
	// LifecycleTarget.Rebuild path; there is no per-kind update code. Rebuild's contract is
	// "redeploy the current artifact + restart" (and, with --build, rebuild the artifact first).
	// kubernetes has no live runtime to rebuild (it is applied out-of-band via kubectl) so it
	// deliberately falls out here with a clear error.
	target, err := ResolveTarget(node, deployName)
	if err != nil {
		return err
	}
	lt, ok := target.(spec.LifecycleTarget)
	if !ok {
		return fmt.Errorf("charly update %s: %q target has no live runtime to rebuild "+
			"(kubernetes is applied out-of-band via `kubectl apply -k` on the rendered Kustomize overlay)",
			deployName, node.Target)
	}
	return lt.Rebuild(context.Background(), spec.DeployTargetRebuildOpts{RebuildImage: c.Build, Tag: c.Tag})
}
