package main

import (
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// arbiter_host.go — the HOST side of the C9 resource-arbiter reverse channel.
//
// The arbiter LOGIC (AcquireExclusive/AcquireShared/ReleaseClaimant/…) moved into the
// COMPILED-IN candy/plugin-preempt (verb:arbiter). Of its original 8 host DEPENDENCIES, ONLY 2
// are genuinely K1-blocked (project-config coupled via LoadUnified) and stay host-side:
// `gather` (gatherPreemptibleHolders) and `resources` (gatherResources). The other 6
// (running/stop[+wait]/start/switchMode/ensureCDI/gpuCDI) moved DIRECTLY into the plugin
// (FLOOR-SLIM-proper Unit-8, candy/plugin-preempt/holder_dispatch.go) — they were reached over
// this seam only because their ORIGINAL implementation used charly-core-private mechanisms
// (providerRegistry / connectPluginByWordRef), which now dispatch instead via the
// class-agnostic sdk.Executor.InvokeProvider (spike-proven live for the builder class earlier
// in this program) — never because the work itself needed a live LoadUnified project.
//
// arbiterHostServer.dispatch is the host handler: it decodes the action-tagged request, runs
// the seam's project-config-coupled implementation, and replies.

// arbiterHostServer carries the seam impls. It is stateless (every seam is a package-level
// host func); the struct exists so the reverse server can hold a non-nil marker + a single
// dispatch entry point.
type arbiterHostServer struct{}

func newArbiterHostServer() *arbiterHostServer { return &arbiterHostServer{} }

// dispatch runs one arbiter host-seam by action name and returns the marshalled reply. Neither
// remaining seam (gather/resources) takes a request payload, so dispatch takes none either.
func (h *arbiterHostServer) dispatch(action string) ([]byte, error) {
	switch action {
	case spec.ArbiterSeamGather:
		return marshalJSON(spec.ArbiterGatherReply{Holders: h.gather()})
	case spec.ArbiterSeamResources:
		return marshalJSON(spec.ArbiterResourcesReply{Gpu: h.resources()})
	default:
		return nil, fmt.Errorf("arbiter host seam: unknown action %q", action)
	}
}

// gather projects every preemptible holder (config read) into config-free descriptors: the
// PreemptionHolds() tokens, the holderAddrFor() address, and the effective restore policy —
// so the plugin's holdersToStop is pure coordination over spec.HolderDescriptor.
func (h *arbiterHostServer) gather() []spec.HolderDescriptor {
	holders := gatherPreemptibleHolders()
	out := make([]spec.HolderDescriptor, 0, len(holders))
	for _, name := range sortedHolderKeys(holders) {
		node := holders[name]
		out = append(out, spec.HolderDescriptor{
			Name:    name,
			Holds:   node.PreemptionHolds(),
			Addr:    holderAddrFor(name, node),
			Restore: deploykit.PreemptEffectiveRestore(node.Preemptible),
		})
	}
	return out
}

// resources projects the project resource map to gpu-backed tokens -> PCI vendor (the only
// thing the plugin's applyMode / firstPoisonedToken need). An arbitration-only token is
// omitted (no device to flip).
func (h *arbiterHostServer) resources() map[string]string {
	out := map[string]string{}
	for tok, rdef := range gatherResources() {
		if rdef != nil && rdef.Gpu != nil {
			out[tok] = rdef.Gpu.Vendor
		}
	}
	return out
}
