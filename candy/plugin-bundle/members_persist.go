package bundle

// members_persist.go — the operator-path MEMBER deploy-override PERSIST, plugin-side (#55 coneC-dsh β1).
// The former host-side per-member persist (charly/bundle_members.go's bringUpMembers →
// persistBedDeployOverrides) shed from charly core; the plugin-bundle walk now persists each sibling
// member PLUGIN-SIDE BEFORE calling the host "deploy-members-up" seam (which runs bringUpMembers
// without the persist), so the member's declared port/volume/env overrides + resource-arbitration
// role are seeded by the time the member's own `charly config`/`charly start` runs. Reuses this
// package's existing deployMarshalNode + loadBundleConfig (the loader-threaded Primaries leg + the
// cycle-free loaderkit overlay read — the SAME pattern saveDeployConfig uses). Mirrors
// candy/plugin-check/bed_persist.go's bed member persist (R3).

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// externalInPlaceFromDescent derives a member's externalInPlace classification from its stamped
// Descent — the plugin-reachable equivalent of the host's bedExternalInPlace(target) (which queries
// the live provider registry via isExternalDeploySubstrate). For a LOADED node the registry verdict
// is already stamped into Descent.Venue: the in-place external substrates (android → "parent",
// exampledeploy → "none") are exactly the non-container, non-ssh, non-shell venues; pod/vm/k8s/local
// are NOT in-place (pod/vm/k8s persist, local is skipped by spec.HostRooted inside
// PersistBedDeployOverrides). A nil Descent (never for a loaded node) falls back to false (persist —
// the safe default matching a pod member).
func externalInPlaceFromDescent(node *spec.Deploy) bool {
	if node == nil || node.Descent == nil {
		return false
	}
	v := node.Descent.Venue
	return v == "parent" || v == "none"
}

// persistMemberDeployOverrides seeds the per-host charly.yml with each sibling MEMBER's
// project-declared deploy-shaped overrides (port / volume / env / security / network + the
// resource-arbitration role) PLUGIN-SIDE, BEFORE the host "deploy-members-up" seam runs
// bringUpMembers (which no longer persists itself — #55 coneC-dsh β1). Best-effort (deploykit.
// PersistBedDeployOverrides has no error return; a persist failure does not abort the deploy — the
// member's own `charly config` re-saves the overlay). Mirrors the former bringUpMembers per-member
// persist call. deploykit.PersistBedDeployOverrides internally self-skips a local/host-rooted node
// and an in-place external node, so calling it unconditionally per member is safe.
func persistMemberDeployOverrides(root *spec.BundleNode) {
	if root == nil || len(root.Members) == 0 {
		return
	}
	marshalNode := deployMarshalNode()
	for _, memberKey := range spec.SortedMemberKeys(root.Members) {
		member := root.Members[memberKey]
		if member == nil {
			continue
		}
		deploykit.PersistBedDeployOverrides(memberKey, *member, externalInPlaceFromDescent(member), marshalNode, loadBundleConfig)
	}
}
