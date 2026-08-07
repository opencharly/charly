package fleet

import (
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"

	"github.com/opencharly/sdk/deploykit"
)

// node_fleet_venue_test.go — relocated (in part) from charly/node_fleet_venue_test.go (#55
// decoupling, Batch A): TestResolveDottedAgentProvisionedVenue asserts deploykit.
// ResolveDeployChain/ClassifyTarget directly (sharing stampTestDescents with
// deploy_chain_test.go), zero charly dep. TestFlattenFleetVenues_* is Batch C's concern
// (plugin-loader); TestOverlayRoundTrip_*/TestPersistBedDeployOverrides_GroupBedNotPersisted
// are the AMBIGUOUS bed-persist cluster the orchestrator ruled STAYS in charly (ruling 1) —
// none of those are touched by this batch.

// TestResolveDottedAgentProvisionedVenue (Risk 5b) proves ResolveDeployChain reaches a 3-level
// agent-provisioned venue (vm → pod → pod) written into a scratch deploy-tree map — without a
// live connection (the chain is built, not dialed). This is the unit-level proof the
// coordinator's R10 live bed round-trip will exercise end-to-end. The SCORER half of this test
// (the AI harness's scoring-chain resolver routing the same dotted venue) moved to
// candy/plugin-check/score_live_test.go's TestPluginResolveDottedAgentProvisionedVenue
// (K1-unblock wave arm 3 — the scoring-chain resolver itself moved plugin-side).
func TestResolveDottedAgentProvisionedVenue(t *testing.T) {
	roots := map[string]spec.FleetNode{
		"nested-check-vm": {
			Target:           "vm",
			From:             "nested-check-vm",
			AgentProvisioned: true,
			Children: map[string]*spec.FleetNode{
				"inner-app-pod": {
					Target:           "pod",
					AgentProvisioned: true,
					Children: map[string]*spec.FleetNode{
						"nested-redis-pod": {
							Target:           "pod",
							AgentProvisioned: true,
						},
					},
				},
			},
		},
	}
	const dotted = "nested-check-vm.inner-app-pod.nested-redis-pod"

	leaf, chain, err := deploykit.ResolveDeployChain(stampTestDescents(roots), dotted, kit.ShellExecutor{})
	if err != nil {
		t.Fatalf("ResolveDeployChain(%q): %v", dotted, err)
	}
	if leaf == nil {
		t.Fatalf("ResolveDeployChain(%q): nil leaf", dotted)
	}
	if deploykit.ClassifyTarget(leaf) != "pod" {
		t.Errorf("leaf target = %q, want pod", deploykit.ClassifyTarget(leaf))
	}
	if chain == nil {
		t.Fatalf("ResolveDeployChain(%q): nil chain", dotted)
	}
}
