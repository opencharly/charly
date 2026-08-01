package main

// deploy_tree.go — the recursive tree walker for schema v2 deployments.
//
// Every deployment is a BundleNode that may carry `children:`.
// This file owns the walk-and-dispatch logic that turns the tree into
// a sequence of per-target Emit() calls with the correct ParentExec
// threaded through.
//
// Apply order is pre-order (parents first): the parent's environment
// must exist before its children can run inside it. Delete order is
// post-order (children first): children tear down while the parent
// venue is still alive to accept teardown commands.

import (
	"github.com/opencharly/spec/spec"
)

// deployTraitsFor resolves a substrate word's DECLARED #DeployTraits (P9) from the provider
// registry — the SINGLE plugin-declared source spec.StampDescent stamps onto node.Descent, and
// the on-the-fly resolver nodeTraits falls back to for a synthetic (un-stamped) node. The
// substrate kinds are compiled-in (registered at init), so this resolves EVERYWHERE, including
// project-less commands, with no prescan/schema bump. Returns nil for a word that is not a
// substrate kind (a targetless group, an empty target) → the external-in-place default.
func deployTraitsFor(word string) *spec.DeployTraits {
	prov, ok := providerRegistry.ResolveKind(word)
	if !ok {
		// An external DEPLOY-class substrate (deploy:<word>) is served by a
		// deploy-target plugin, NOT a KIND-class provider, so ResolveKind misses it.
		// Its pluginDeployTarget applies the deploy IN-PLACE and runs its deploy-scope
		// probes host-side via ShellExecutor — the external-in-place venue (the "none"
		// zero value; see #DeployTraits.Venue). Resolving it BY TRAIT (not a kind-word
		// switch) keeps every consult site — checkLocalTarget above all — routing it
		// host-side, as it did under the retired isExternalDeploySubstrate guard.
		if isExternalDeploySubstrate(word) {
			return &spec.DeployTraits{Venue: "none"}
		}
		return nil
	}
	if dc, ok := prov.(spec.DeployTraitsCarrier); ok {
		return dc.DeclaredDeployTraits()
	}
	return nil
}

// effectiveTarget returns the node's substrate word for trait resolution — node.Target with an
// empty target defaulted to "pod" (the loader's empty→pod default, classifyTarget). Used by
// nodeTraits to resolve traits for a synthetic node whose descent was never stamped.
func effectiveTarget(node *spec.BundleNode) string {
	if node == nil || node.Target == "" {
		return "pod"
	}
	return node.Target
}

// nodeTraits returns the node's stamped deploy-descent descriptor — the SINGLE thing every
// consult site reads to branch on substrate behaviour (venue / machine_venue / exclusive_venue /
// image_context / leaf_only), instead of switching on the substrate kind word (the boundary law).
// A loaded node carries a stamped node.Descent; a synthetic node (built outside the loader, e.g.
// classifyTarget) has none, so its traits are resolved on the fly from the registry. Never nil.
func nodeTraits(node *spec.BundleNode) *spec.DescentDescriptor {
	if node != nil && node.Descent != nil {
		return node.Descent
	}
	return spec.DescentFromTraits(deployTraitsFor(effectiveTarget(node)))
}

// deployTraitDescent is the WORD-level analogue of nodeTraits (P9): it resolves a substrate
// word's DECLARED traits from the registry and returns the derived descent descriptor, for the
// few consult sites that hold only a substrate word (not a node). Never nil.
func deployTraitDescent(word string) *spec.DescentDescriptor {
	return spec.DescentFromTraits(deployTraitsFor(word))
}

// NestedContainerName computes the podman container name used when
// a container node is nested under a dotted path. Path segments are
// joined with underscores so the result is a legal podman name.
// Called by the walker when it knows the full dotted path.
//
// The host-side merged-tree read (project charly.yml + per-host overlay) that
// formerly lived here moved to check_cmd.go's resolveMergedDeployTree (#55 LOADER
// cone) so this file — pure kind-blind trait/descent loader reads (clause M/D) —
// no longer imports sdk/deploykit.
