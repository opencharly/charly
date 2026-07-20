package main

// node_bundle_venue.go — venue-from-position for bundle plan steps.
//
// In the unified node-form model a step's EXECUTION VENUE comes ENTIRELY from
// its POSITION in the bundle tree — there is no authored `on:`/`pod:` override
// (both retired). flattenBundleVenues runs at load time, AFTER the tree is
// built (buildBundleNode) and BEFORE foldMembers + validation, and:
//
//   1. stamps every step's `venue` (Op.Venue) from its tree position:
//        - a step directly under a WORKLOAD root R          → "R"
//        - a step under a sibling MEMBER M                  → "M"            (bare)
//        - a step under a NESTED child C of parent path P   → "P.C"         (dotted)
//   2. HOISTS every member/child step into the ROOT bundle's flat Plan (and
//      clears the member/child Plan), because both runner entry points read
//      the root node.Plan: checkrun.go runOne (deterministic check-live,
//      per-step venue swap) and check_runner_live.go (iterate scoring, venue
//      bucketing). The stamped venue resolves to a live executor via
//      resolveScoringChain / ResolveDeployChain / the run TargetResolver.
//
// A direct step under a pure GROUP root (no workload container) is a hard
// error — a group has no venue of its own; place the step under a member.

import (
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// flattenBundleVenues stamps venue + hoists plan steps for every top-level
// bundle in uf. Idempotent on an already-flattened tree (members/children have
// empty Plan after the first pass, so re-running hoists nothing). Must run
// before foldMembers (which promotes members to top-level, mutating the map)
// and before validateCheckBeds/validateIterateBed (which count root Plan
// checks).
func flattenBundleVenues(uf *UnifiedFile) error {
	if uf == nil || len(uf.Bundle) == 0 {
		return nil
	}
	for _, name := range sortedDeployKeys(uf.Bundle) {
		node := uf.Bundle[name]
		if err := flattenBundleOne(&node, name); err != nil {
			return err
		}
		uf.Bundle[name] = node
	}
	return nil
}

// flattenBundleOne flattens a single top-level bundle tree rooted at `root`
// (named rootName) in place.
func flattenBundleOne(root *spec.BundleNode, rootName string) error {
	// 1. Root's OWN direct steps run on the root's own venue (its container /
	//    host). A pure GROUP root (no cross-ref → empty Target) has no container,
	//    so a direct scored/run step there has nowhere to run.
	if root.Target == "" && len(root.Plan) > 0 {
		return fmt.Errorf("bundle %q is a group (no workload cross-ref) but carries %d direct plan step(s) — a group has no venue; place each step under a member/nested resource node", rootName, len(root.Plan))
	}
	for i := range root.Plan {
		root.Plan[i].Venue = rootName
	}
	// 2. Members (siblings) are addressed by their BARE name (foldMembers
	//    promotes them to top-level; an agent-provisioned member resolves via
	//    the bare `charly-<name>` fallback). Nested children of the ROOT
	//    workload are addressed `rootName.child`.
	for _, mName := range sortedMemberKeys(root.Members) {
		hoistVenueSubtree(root, root.Members[mName], mName)
	}
	for _, cName := range deploykit.SortedNestedKeys(root.Children) {
		hoistVenueSubtree(root, root.Children[cName], rootName+"."+cName)
	}
	return nil
}

// hoistVenueSubtree stamps venuePath onto every step of `node`, appends those
// steps to root.Plan, clears node.Plan (so the steps run once, from the root
// plan), and recurses into node's nested children (dotted) and any sub-members
// (bare). venuePath is the dotted address that resolveScoringChain /
// ResolveDeployChain resolve.
func hoistVenueSubtree(root, node *spec.BundleNode, venuePath string) {
	if node == nil {
		return
	}
	for i := range node.Plan {
		s := node.Plan[i]
		s.Venue = venuePath
		root.Plan = append(root.Plan, s)
	}
	node.Plan = nil
	for _, cName := range deploykit.SortedNestedKeys(node.Children) {
		hoistVenueSubtree(root, node.Children[cName], venuePath+"."+cName)
	}
	// A member that is itself a group can carry sibling members — addressed
	// bare (defensive; the shipped beds nest via Children only).
	for _, mName := range sortedMemberKeys(node.Members) {
		hoistVenueSubtree(root, node.Members[mName], mName)
	}
}

// venueIsAgentProvisioned reports whether the bare top-level venue name resolves
// to an agent-provisioned member/child anywhere in uf's bundle trees. Used by
// the host-target image preflight to SKIP venues whose image the AI builds
// in-run (they are not pullable). Agent-provisioned members are not folded to
// top-level, so the lookup walks each bed's in-tree members/children.
func venueIsAgentProvisioned(uf *UnifiedFile, venue string) bool {
	if uf == nil || venue == "" {
		return false
	}
	var walk func(n *spec.BundleNode) bool
	walk = func(n *spec.BundleNode) bool {
		if n == nil {
			return false
		}
		for k, child := range n.Children {
			if k == venue && child.AgentProvisioned {
				return true
			}
			if walk(child) {
				return true
			}
		}
		for k, member := range n.Members {
			if k == venue && member.AgentProvisioned {
				return true
			}
			if walk(member) {
				return true
			}
		}
		return false
	}
	for _, name := range sortedDeployKeys(uf.Bundle) {
		node := uf.Bundle[name]
		if walk(&node) {
			return true
		}
	}
	return false
}
