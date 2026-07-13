package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk/enginekit"
	"github.com/opencharly/sdk/spec"
)

// arbiter_host.go — the HOST side of the C9 resource-arbiter reverse channel.
//
// The arbiter LOGIC (AcquireExclusive/AcquireShared/ReleaseClaimant/…) moved into the
// COMPILED-IN candy/plugin-preempt (verb:arbiter). Its 7 host DEPENDENCIES — the things it
// cannot hold across the module boundary (the project config, the VM/pod lifecycle, the GPU
// driver flip) — STAY host-side and are reached mid-logic over ExecutorService.HostArbiter.
// arbiterHostServer.dispatch is the host handler: it decodes the action-tagged request, runs
// the seam's CURRENT in-core default implementation, and replies. Most are the SAME funcs the
// former in-core ResourceArbiter injected as its seams (gather/running/stop/start/resources/
// switchMode/ensureCDI) — nothing moved, only the caller (now the plugin, over the wire). gpuCDI
// (gpuCDIHolders) is the one seam ADDED for the C9 GPU-reclaim: it enumerates running charly
// GPU-CDI pods so the arbiter can stop a leaked/stray one before a nvidia->vfio flip.
//
// The `stop` seam FOLDS the wait-until-stopped (holderStop + waitStoppedHost): the readiness
// StopGate + pollUntil stay host-side, so NO readiness machinery moves into the plugin — the wait
// is part of "free the resource".

// arbiterHostServer carries the seam impls. It is stateless (every seam is a package-level
// host func); the struct exists so the reverse server can hold a non-nil marker + a single
// dispatch entry point.
type arbiterHostServer struct{}

func newArbiterHostServer() *arbiterHostServer { return &arbiterHostServer{} }

// dispatch runs one arbiter host-seam by action name and returns the marshalled reply.
func (h *arbiterHostServer) dispatch(action string, params []byte) ([]byte, error) {
	switch action {
	case spec.ArbiterSeamGather:
		return marshalJSON(spec.ArbiterGatherReply{Holders: h.gather()})
	case spec.ArbiterSeamResources:
		return marshalJSON(spec.ArbiterResourcesReply{Gpu: h.resources()})
	case spec.ArbiterSeamRunning:
		var req spec.ArbiterHolderReq
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("arbiter running seam: decode: %w", err)
		}
		return marshalJSON(spec.ArbiterBoolReply{Bool: holderRunning(req.Addr)})
	case spec.ArbiterSeamStop:
		var req spec.ArbiterHolderReq
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("arbiter stop seam: decode: %w", err)
		}
		return marshalJSON(spec.ArbiterErrReply{Error: errString(h.stopAndWait(req.Addr))})
	case spec.ArbiterSeamStart:
		var req spec.ArbiterHolderReq
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("arbiter start seam: decode: %w", err)
		}
		return marshalJSON(spec.ArbiterErrReply{Error: errString(holderStart(req.Addr))})
	case spec.ArbiterSeamSwitch:
		var req spec.ArbiterSwitchReq
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("arbiter switchMode seam: decode: %w", err)
		}
		wedged, err := gpuSwitchModeTolerant(req.Vendor, req.Mode)
		return marshalJSON(spec.ArbiterSwitchReply{Error: errString(err), Wedged: wedged})
	case spec.ArbiterSeamEnsureCDI:
		ensureCDIRoot()
		return marshalJSON(spec.ArbiterErrReply{})
	case spec.ArbiterSeamGpuCDI:
		return marshalJSON(spec.ArbiterGpuCDIReply{Holders: h.gpuCDIHolders()})
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
			Restore: preemptEffectiveRestore(node.Preemptible),
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

// stopAndWait gracefully stops a holder AND waits until it is actually powered off (the
// resource is truly freed) — the folded stop seam. The readiness StopGate + pollUntil stay
// host-side (waitStoppedHost), so the plugin never sees readiness config.
func (h *arbiterHostServer) stopAndWait(addr spec.HolderAddr) error {
	if err := holderStop(addr); err != nil {
		return err
	}
	if !waitStoppedHost(addr) {
		return fmt.Errorf("holder %q did not reach a stopped state within the stop grace (resource not freed)", addr.Name)
	}
	return nil
}

// gpuCDIHolders lists every RUNNING charly-<deploy> pod container that holds the nvidia GPU as a CDI
// device — the reclaim seam. It reuses the status EngineClient's batched ps+inspect (SnapshotAll),
// so it sees a container's GPU device the /proc/*/fd holder-scan never can. The deploy name is the
// `charly-` prefix stripped off; `Base` = that name reconstructs the same container for the stop
// seam (stopPodService(Base,"") -> charly-<Base>), for both plain and instance deploys.
func (h *arbiterHostServer) gpuCDIHolders() []spec.HolderAddr {
	rt, err := ResolveRuntime()
	if err != nil {
		return nil
	}
	snaps, err := enginekit.NewEngineClient(rt.RunEngine).SnapshotAll(false) // running only, resolved run engine
	if err != nil {
		return nil
	}
	var out []spec.HolderAddr
	for _, s := range snaps {
		if s.State != "running" || !devicesHoldNvidiaGPU(s.Devices) {
			continue
		}
		deploy := strings.TrimPrefix(s.Name, "charly-")
		out = append(out, spec.HolderAddr{Name: deploy, Target: "pod", Base: deploy})
	}
	return out
}

// devicesHoldNvidiaGPU reports whether a container's inspected device list carries the nvidia GPU —
// the CDI name (`nvidia.com/gpu…`) or a `/dev/nvidia*` node.
func devicesHoldNvidiaGPU(devices []string) bool {
	for _, d := range devices {
		if strings.Contains(d, "nvidia.com/gpu") || strings.HasPrefix(d, "/dev/nvidia") {
			return true
		}
	}
	return false
}
