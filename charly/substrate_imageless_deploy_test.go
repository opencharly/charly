package main

// substrate_imageless_deploy_test.go — the RCA repro for the runtime envelope drop
// (2026-09-07): the canonical imageless agent_provisioned pod (agent_provisioned: true +
// an iterate: block, NO image:/from:, NO resource-member sibling) VANISHED from the
// resolved-project envelope's Deploy map — every build:project consumer saw "no entity".
//
// Root cause: foldSubstrateKind's deploy-vs-template classification
// (pl.IsDeployShape(pn) || len(pl.ResourceChildren(pn)) > 0) recognized only from:/image:
// bodies and resource-member children. The imageless agent_provisioned spelling — the
// shape spec.ValidateDeploymentTree → ValidateDeployRequiresBox EXEMPTS from the pod
// box requirement — matched neither arm, so the node folded as a standalone TEMPLATE
// (acc.PluginKinds["pod"][name]) and never entered acc.Deploy. The plugin-build #9
// in-repo tests missed it because every test doc carries a watcher: sibling whose pod:
// disc feeds the ResourceChildren fallback arm. The fix folded BOTH extra arms into the
// ONE loaderkit.IsDeployShape classifier (parser consolidation F1.2/F1.4) — the member
// channel (any resource-member child — the ONE memberDisc/IsResourceDisc predicate) and
// the agent_provisioned body probe now live beside the from:/image: scan.
//
// The contract: the shape classifier predicate MUST match the Deploy gate's
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

// TestSubstrateKind_ImagelessAgentProvisionedPodFoldsToDeploy is the runtime-drop repro:
// the minimal imageless agent_provisioned pod must fold into acc.Deploy (the deploy arm),
// never into the pod template map.
func TestSubstrateKind_ImagelessAgentProvisionedPodFoldsToDeploy(t *testing.T) {
	pn := singleParsedNode(t, imagelessAgentProvisionedMinimalDoc)
	prov, ok := providerRegistry.ResolveKind("pod")
	if !ok {
		t.Fatal("pod kind must resolve to the compiled-in candy/plugin-substrate provider")
	}
	var acc spec.MaterializedProject
	if err := foldSubstrateKind(prov, pn, &acc); err != nil {
		t.Fatalf("foldSubstrateKind: %v", err)
	}
	bn, ok := acc.Deploy["agent-live"]
	if !ok {
		t.Fatalf("imageless agent_provisioned pod DROPPED from acc.Deploy (deploy keys %v); pod template map: %v",
			deployKeysForAcc(&acc), templateKeysForAcc(&acc, "pod"))
	}
	if !bn.AgentProvisioned {
		t.Fatalf("folded DeployNode lost agent_provisioned: %+v", bn)
	}
	if bn.Iterate == nil || bn.Iterate.Sandbox != "agent-sandbox" {
		t.Fatalf("folded DeployNode lost the iterate data: %+v", bn.Iterate)
	}
	if acc.PluginKinds["pod"]["agent-live"] != nil {
		t.Fatal("imageless agent_provisioned pod ALSO folded into the pod template map — must be acc.Deploy ONLY")
	}
}

// TestSubstrateKind_PlainImagelessPodStaysTemplate is the discriminator: WITHOUT the flag
// the same imageless body is NOT a deploy shape — it keeps folding as a standalone
// template (the Deploy gate rejects it only if it ever reaches a Deploy; the shape
// classifier must not widen past the exemption).
func TestSubstrateKind_PlainImagelessPodStaysTemplate(t *testing.T) {
	const doc = `bare-pod:
    pod:
        iterate:
            sandbox: agent-sandbox
`
	pn := singleParsedNode(t, doc)
	prov, ok := providerRegistry.ResolveKind("pod")
	if !ok {
		t.Fatal("pod kind must resolve to the compiled-in candy/plugin-substrate provider")
	}
	var acc spec.MaterializedProject
	if err := foldSubstrateKind(prov, pn, &acc); err != nil {
		t.Fatalf("foldSubstrateKind: %v", err)
	}
	if _, ok := acc.Deploy["bare-pod"]; ok {
		t.Fatal("plain imageless pod (no agent_provisioned) folded into acc.Deploy — the classifier must not widen past the exemption")
	}
	if acc.PluginKinds["pod"]["bare-pod"] == nil {
		t.Fatalf("plain imageless pod folded nowhere: deploy %v, pod templates %v",
			deployKeysForAcc(&acc), templateKeysForAcc(&acc, "pod"))
	}
}

// TestSubstrateKind_ImagelessAgentProvisionedWithSiblingStillFolds locks the plugin-build
// #9 shape (the watcher-sibling doc) against regression: the deploy-level sibling keeps
// feeding the member channel of the ONE IsDeployShape classifier, and the parent still
// folds to acc.Deploy.
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
	pn := singleParsedNode(t, doc)
	prov, ok := providerRegistry.ResolveKind("pod")
	if !ok {
		t.Fatal("pod kind must resolve to the compiled-in candy/plugin-substrate provider")
	}
	var acc spec.MaterializedProject
	if err := foldSubstrateKind(prov, pn, &acc); err != nil {
		t.Fatalf("foldSubstrateKind: %v", err)
	}
	bn, ok := acc.Deploy["check-agent-live"]
	if !ok {
		t.Fatalf("watcher-sibling agent_provisioned pod dropped from acc.Deploy (keys %v)", deployKeysForAcc(&acc))
	}
	if len(bn.Member) != 1 || bn.Member[0].Name != "watcher" {
		t.Fatalf("member tree = %+v, want the deploy-level sibling watcher", bn.Member)
	}
}

// templateKeysForAcc mirrors deployKeysForAcc for the generic standalone-template map
// (acc.PluginKinds[disc]).
func templateKeysForAcc(acc *spec.MaterializedProject, disc string) []string {
	out := make([]string, 0, len(acc.PluginKinds[disc]))
	for k := range acc.PluginKinds[disc] {
		out = append(out, k)
	}
	return out
}
