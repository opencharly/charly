package main

// Deploy-name resolution + per-target dispatch for `charly update`.
//
// `charly update <name>` resolves a deploy name (VM/local/pod targets all
// dispatch from here) or a bare image name; this file consolidates the
// per-target dispatch into podUpdateCmd (the host-side reconstruction of the
// former UpdateCmd — now command:update in candy/plugin-pod) so the
// user-facing surface is just one verb.
//
// CONFIRMED STAY (#55 W3 A5, superseding this file's own prior "straightforward-but-large
// RELOCATION work" framing — that framing predates the later Cutover B ruling below and never
// got corrected here; the file's own header is a CLAIM, not a verdict, until re-verified). The
// K1-UNBLOCK-era venue-executor-session blocker it described IS resolved (InvokeProvider's
// VenueDescriptorJson self-description + specexec.RootExecutorForDeployNode are both live,
// portable primitives) — but dispatchByDeployTarget's orchestration body itself does NOT move,
// because it CALLS two irreducible core-private M-mechanisms directly, not a generic dispatch a
// plugin could reach:
//   - loadDeployPlugins (plugin_loader.go) IS the plugin-loading mechanism — it mutates the
//     core-private provider registry by connecting NEW out-of-process plugins. A plugin cannot
//     load another plugin into the host's own registry; that registry is core-private by
//     definition (the K1 loader keystone).
//   - ResolveTarget (unified_targets.go) reads providerRegistry.ResolveDeploy(node.Target) and
//     type-asserts the result to the core-private *grpcProvider — already explicitly named STAY
//     by the orchestrator adjudication (KERNEL_MANIFEST.md's unified_targets.go table).
//
// This is the EXACT same "one step that cannot cross the plugin boundary" pattern
// pod_lifecycle_verb.go's dispatchLifecycleTarget already established for start/stop/shell/logs/
// service/cmd — host_build_pod_lifecycle_dispatch.go's Cutover B header already ruled this file
// "keeps its existing podUpdateCmd/dispatchByDeployTarget body UNCHANGED... registry+loader-coupled
// the same way", charly/commands.go's header independently confirms it, and charly.yml's own
// check-fedora-pod bed plan asserts it live ("dispatchByDeployTarget... UNCHANGED by this
// cutover"). Four independent sibling sources already agreed; only THIS file's own header hadn't
// caught up. resolveUpdateDeployNode/noteUpdateDisposability are pure spec-native helpers (no
// core-private coupling) but are too small and too tightly sequenced with the STAY body to be
// worth a wire-shape change for (LOC-neutral churn, the same "not a real reduction" verdict the
// A9 ruling gave unified_targets.go's per-verb methods).
//
// Critical semantic: NONE of the dispatchers below regenerate the
// user-overlay deploy entry (no `charly bundle add` / `charly config` calls
// allowed in the pod path). The user's directive: "Any config changes
// should be done via charly config only." This verb updates ARTIFACTS
// (image bits, VM disk, local candies, quadlet/marker image refs);
// `charly config` updates CONFIG. The two responsibilities are strictly
// separated.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/spec/spec"
)

// dispatchByDeployTarget resolves c.Box as a charly.yml entry and
// invokes the target-specific update helper. Errors explicitly when:
//
//   - cwd has no charly.yml (use 'charly box pull' for image-only refresh)
//   - the name doesn't resolve to a deploy entry (same)
//   - the deploy entry's `box:` field is empty for pod targets
//     (config bug — deploy needs to know which image to refresh)
//   - target is unknown / unsupported (k8s)
//
// No silent fallbacks. The user gets a clear error pointing at the
// right alternative or the field they need to fix.
// resolveUpdateDeployNode looks up the deploy entry for an `charly update`
// invocation by the FULL deploy key. deployKey applies the -i instance,
// returning the bare (or dotted-nested) name unchanged when instance is
// empty — so `charly update <base> -i <inst>` finds the instance-keyed
// `<base>/<inst>` entry, plain names still resolve, and dotted nested
// paths (`a.b.c`) still walk. Mirrors the composition `charly start` uses via
// dc.Lookup(c.Box, c.Instance). On miss the error reports the full key.
func resolveUpdateDeployNode(tree map[string]spec.BundleNode, image, instance string) (*spec.BundleNode, error) {
	key := spec.DeployKey(image, instance)
	node, _, err := spec.ResolveNodePath(tree, key)
	if err != nil || node == nil {
		return nil, fmt.Errorf("no deploy named %q in charly.yml. To refresh an image artifact only, use 'charly box pull %s'", key, image)
	}
	return node, nil
}

func (c *podUpdateCmd) dispatchByDeployTarget() error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	// command:update (plugin-pod) resolved the merged deploy tree PLUGIN-SIDE
	// (loaderkit.ResolveMergedTreeViaExecutor) and threaded it in — consume it instead of
	// re-loading the tree host-side (#55 Cone A Unit 3b). An absent/empty tree yields the
	// same "no charly.yml" error a nil host-tree-read result produced.
	var tree map[string]spec.BundleNode
	if len(c.TreeJSON) > 0 {
		if err := json.Unmarshal(c.TreeJSON, &tree); err != nil {
			return fmt.Errorf("decoding threaded deploy tree: %w", err)
		}
	}
	if tree == nil {
		return fmt.Errorf("no charly.yml found relative to %s; charly update requires a deploy name. To refresh an image artifact only, use 'charly box pull %s'", dir, c.Box)
	}
	node, err := resolveUpdateDeployNode(tree, c.Box, c.Instance)
	if err != nil {
		return err
	}

	// `charly update` obeys an EXPLICIT invocation on ANY target — the tool is
	// fully capable; the disposable-only constraint is a discipline on the AI's
	// AUTONOMOUS action (CLAUDE.md R10 + /charly-internals:disposable) and on the
	// check-runner's unattended fresh-rebuild (validateCheckBeds), NOT a capability
	// limit on this human-driven verb. For a non-disposable target we print a
	// one-line transparency note (the operator may have mistyped a name) and
	// proceed; we never refuse.
	noteUpdateDisposability(node, c.Box, c.Instance)

	// Normalize legacy target spellings before resolution. Empty / "container"
	// both mean "pod" (the schema invariant requires target:, so empty is only
	// pre-migration defensiveness).
	if node.Target == "" || node.Target == "container" {
		node.Target = "pod"
	}
	deployName := c.Box

	// Connect the deployment's OUT-OF-TREE plugins before ResolveTarget, so an
	// external deploy SUBSTRATE (the E3-deploy plugin-side deploy target) resolves its
	// grpcProvider for the rebuild — the SAME loadDeployPlugins bundle add / bundle
	// del use (R3).
	if err := loadDeployPlugins(dir, deployName, nil); err != nil {
		return err
	}

	// UNIFIED dispatch — charly update for EVERY kind routes through the SAME
	// ResolveTarget → LifecycleTarget.Rebuild path; there is no per-kind update
	// code. Rebuild's contract is "redeploy the current artifact + restart"
	// (and, with --build, rebuild the artifact first); each kind's adapter
	// realizes it for its substrate (vm: destroy→create the domain, then
	// re-apply the deploy node's candies via deploy add; pod: deploy
	// add→config→start; local: re-apply candies). k8s has no live runtime
	// to rebuild (it is applied out-of-band via kubectl) so it is deliberately
	// NOT a LifecycleTarget and falls out here with a clear error.
	target, err := ResolveTarget(node, deployName)
	if err != nil {
		return err
	}
	lt, ok := target.(LifecycleTarget)
	if !ok {
		return fmt.Errorf("charly update %s: %q target has no live runtime to rebuild "+
			"(k8s is applied out-of-band via `kubectl apply -k` on the rendered Kustomize overlay)",
			deployName, node.Target)
	}
	return lt.Rebuild(context.Background(), RebuildOpts{RebuildImage: c.Build})
}

// noteUpdateDisposability prints a one-line transparency note when an EXPLICIT
// `charly update` targets a deploy that is NOT marked `disposable: true` (and not
// ephemeral — see IsDisposable() for the implication chain). It NEVER refuses:
// `charly update` is a human-driven verb that obeys any explicit invocation on any
// target. The `disposable:` flag remains load-bearing as the authorization for
// the AI's AUTONOMOUS destroy + rebuild (CLAUDE.md R10) and for the check-runner's
// unattended fresh-rebuild (validateCheckBeds) — it just no longer gates this
// command. The note lets an operator catch a mistyped name before the rebuild
// proceeds.
//
// Cross-kind name reuse is permitted, so the user-facing key includes the
// instance suffix when present (the deployKey form matches charly.yml + what the
// operator typed).
func noteUpdateDisposability(node *spec.BundleNode, image, instance string) {
	if node == nil || node.IsDisposable() {
		return
	}
	key := spec.DeployKey(image, instance)
	lifecycle := node.Lifecycle
	if lifecycle == "" {
		lifecycle = "(unset)"
	}
	fmt.Fprintf(os.Stderr,
		"Note: %q is not marked `disposable: true` (lifecycle: %s); rebuilding it anyway per your explicit `charly update`.\n",
		key, lifecycle)
}
