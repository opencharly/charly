package main

// substrate_imageless_deploy_test.go — the RCA repro for the runtime envelope drop
// (2026-09-07): the canonical imageless agent_provisioned pod (agent_provisioned: true +
// an iterate: block, NO image:/from:, NO resource-member sibling) VANISHED from the
// resolved-project envelope's Deploy map — every build:project consumer saw "no entity".
//
// Root cause: foldSubstrateKind's deploy-vs-template classification
// (pl.IsDeployShape(pn) || len(pl.ResourceChildren(pn)) > 0) recognizes only from:/image:
// bodies and resource-member children. The imageless agent_provisioned spelling — the
// shape spec.ValidateDeploymentTree → ValidateDeployRequiresBox EXEMPTS from the pod
// box requirement — matched neither arm, so the node folded as a standalone TEMPLATE
// (acc.PluginKinds["pod"][name]) and never entered acc.Fleet. The plugin-build #9
// in-repo tests missed it because every test doc carries a watcher: sibling whose pod:
// disc feeds the ResourceChildren fallback arm.
//
// The contract: the shape classifier predicate MUST match the Fleet gate's
// agent_provisioned exemption exactly — an agent_provisioned body IS a deploy shape.

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// the minimal imageless repro: ONE pod node, no image, no member sibling.
const imagelessAgentProvisionedMinimalDoc = `agent-live:
    pod:
        agent_provisioned: true
        iterate:
            sandbox: agent-sandbox
            agent: [agent-live-claude]
            plateau_iteration: 1
            prompt: Reply with one short acknowledgement.
            note: false
            env: {}
`

// TestSubstrateKind_ImagelessAgentProvisionedPodFoldsToFleet is the runtime-drop repro:
// the minimal imageless agent_provisioned pod must fold into acc.Fleet (the deploy arm),
// never into the pod template map.
func TestSubstrateKind_ImagelessAgentProvisionedPodFoldsToFleet(t *testing.T) {
	gn := substrateNodeFromYAML(t, imagelessAgentProvisionedMinimalDoc)
	pn, err := genericToParsedNode(gn)
	if err != nil {
		t.Fatalf("genericToParsedNode: %v", err)
	}
	prov, ok := providerRegistry.ResolveKind("pod")
	if !ok {
		t.Fatal("pod kind must resolve to the compiled-in candy/plugin-substrate provider")
	}
	var acc spec.MaterializedProject
	if err := foldSubstrateKind(prov, pn, &acc); err != nil {
		t.Fatalf("foldSubstrateKind: %v", err)
	}
	bn, ok := acc.Fleet["agent-live"]
	if !ok {
		t.Fatalf("imageless agent_provisioned pod DROPPED from acc.Fleet (fleet keys %v); pod template map: %v",
			fleetKeysForAcc(&acc), templateKeysForAcc(&acc, "pod"))
	}
	if !bn.AgentProvisioned {
		t.Fatalf("folded FleetNode lost agent_provisioned: %+v", bn)
	}
	if bn.Iterate == nil || bn.Iterate.Sandbox != "agent-sandbox" {
		t.Fatalf("folded FleetNode lost the iterate data: %+v", bn.Iterate)
	}
	if acc.PluginKinds["pod"]["agent-live"] != nil {
		t.Fatal("imageless agent_provisioned pod ALSO folded into the pod template map — must be acc.Fleet ONLY")
	}
}

// TestSubstrateKind_PlainImagelessPodStaysTemplate is the discriminator: WITHOUT the flag
// the same imageless body is NOT a deploy shape — it keeps folding as a standalone
// template (the Fleet gate rejects it only if it ever reaches a Fleet; the shape
// classifier must not widen past the exemption).
func TestSubstrateKind_PlainImagelessPodStaysTemplate(t *testing.T) {
	const doc = `bare-pod:
    pod:
        iterate:
            sandbox: agent-sandbox
`
	gn := substrateNodeFromYAML(t, doc)
	pn, err := genericToParsedNode(gn)
	if err != nil {
		t.Fatalf("genericToParsedNode: %v", err)
	}
	prov, ok := providerRegistry.ResolveKind("pod")
	if !ok {
		t.Fatal("pod kind must resolve to the compiled-in candy/plugin-substrate provider")
	}
	var acc spec.MaterializedProject
	if err := foldSubstrateKind(prov, pn, &acc); err != nil {
		t.Fatalf("foldSubstrateKind: %v", err)
	}
	if _, ok := acc.Fleet["bare-pod"]; ok {
		t.Fatal("plain imageless pod (no agent_provisioned) folded into acc.Fleet — the classifier must not widen past the exemption")
	}
	if acc.PluginKinds["pod"]["bare-pod"] == nil {
		t.Fatalf("plain imageless pod folded nowhere: fleet %v, pod templates %v",
			fleetKeysForAcc(&acc), templateKeysForAcc(&acc, "pod"))
	}
}

// TestSubstrateKind_ImagelessAgentProvisionedWithSiblingStillFolds locks the plugin-build
// #9 shape (the watcher-sibling doc) against regression: the deploy-level sibling keeps
// feeding the ResourceChildren arm, and the parent still folds to acc.Fleet.
func TestSubstrateKind_ImagelessAgentProvisionedWithSiblingStillFolds(t *testing.T) {
	const doc = `check-agent-live:
    pod:
        agent_provisioned: true
        iterate:
            sandbox: check-agent-pod
            agent: [check-agent-live-claude]
            plateau_iteration: 1
            prompt: Reply with one short acknowledgement.
            note: false
            env: {}
    watcher:
        pod:
            image: watcher-img
`
	gn := substrateNodeFromYAML(t, doc)
	pn, err := genericToParsedNode(gn)
	if err != nil {
		t.Fatalf("genericToParsedNode: %v", err)
	}
	prov, ok := providerRegistry.ResolveKind("pod")
	if !ok {
		t.Fatal("pod kind must resolve to the compiled-in candy/plugin-substrate provider")
	}
	var acc spec.MaterializedProject
	if err := foldSubstrateKind(prov, pn, &acc); err != nil {
		t.Fatalf("foldSubstrateKind: %v", err)
	}
	bn, ok := acc.Fleet["check-agent-live"]
	if !ok {
		t.Fatalf("watcher-sibling agent_provisioned pod dropped from acc.Fleet (keys %v)", fleetKeysForAcc(&acc))
	}
	if len(bn.Member) != 1 || bn.Member[0].Name != "watcher" {
		t.Fatalf("member tree = %+v, want the deploy-level sibling watcher", bn.Member)
	}
}

// templateKeysForAcc mirrors fleetKeysForAcc for the generic standalone-template map
// (acc.PluginKinds[disc]).
func templateKeysForAcc(acc *spec.MaterializedProject, disc string) []string {
	out := []string{}
	for k := range acc.PluginKinds[disc] {
		out = append(out, k)
	}
	return out
}
