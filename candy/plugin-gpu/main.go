// Package gpu — the OpRun Invoke entrypoint. charly's in-core Detect* / EnsureCDI /
// MemlockLimitBytes / VfioGroupAccessible / detectAMDGFXVersion shims (gpu_shim.go)
// resolve verb:gpu and Invoke OpRun with a spec.GpuProbeInput whose Action selects the
// host probe; this provider runs the matching sysfs/exec detection and returns a
// spec.GpuProbeReply. The three static data tables ride in on the input (they stay in
// charly's embedded charly.yml — see detect.go for the carve-out rationale).
package gpu

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var describeSchemaFS embed.FS

const calver = "2026.182.0001"

// NewProvider builds the gpu provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct {
	pb.UnimplementedProviderServer
}

// Invoke handles OpRun: decode the spec.GpuProbeInput, run the action's host probe, and
// return the spec.GpuProbeReply as JSON.
func (p *provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("gpu: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	// verb:gpu multiplexes TWO disjoint action vocabularies on OpRun: the C11 DETECTION
	// actions (spec.GpuProbeInput) and the C9 DRIVER-SWITCH actions (spec.GpuSwitchInput).
	// Peek the action to pick the decoder; the switch actions route to invokeSwitch.
	var peek struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(req.GetParamsJson(), &peek); err != nil {
		return nil, fmt.Errorf("gpu: decode action: %w", err)
	}
	switch peek.Action {
	case spec.GpuSwitchActionMode, spec.GpuSwitchActionEnsureCDI, spec.GpuSwitchActionWedgeDetected,
		spec.GpuSwitchActionGroupInMode, spec.GpuSwitchActionCurrentMode, spec.GpuSwitchActionDisplayDriver,
		spec.GpuSwitchActionPlan:
		return invokeSwitch(req.GetParamsJson())
	}
	var in spec.GpuProbeInput
	if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
		return nil, fmt.Errorf("gpu: decode input: %w", err)
	}
	var reply spec.GpuProbeReply
	switch in.Action {
	case "detect-gpu":
		reply.Bool = defaultDetectGPU()
	case "detect-amd-gpu":
		reply.Bool = defaultDetectAMDGPU()
	case "detect-vfio":
		rep := defaultDetectVFIO(in.PCIClassLabels)
		reply.Vfio = &rep
	case "detect-host-devices":
		dd := defaultDetectHostDevices(in.DevicePatterns, in.GpuVendors)
		reply.HostDevices = &dd
	case "ensure-cdi":
		ensureCDI()
	case "memlock":
		reply.MemlockSoft, reply.MemlockHard = memlockLimitBytes()
	case "vfio-group-accessible":
		reply.Bool = vfioGroupAccessible(in.Group)
	case "amd-gfx-version":
		reply.Str = detectAMDGFXVersion()
	default:
		return nil, fmt.Errorf("gpu: unknown action %q", in.Action)
	}
	out, err := json.Marshal(reply)
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises verb:gpu serving OpRun. The verb is invoked with the structured
// spec.GpuProbeInput, not an authored plugin_input, so it declares no #*Input — Describe
// ships only the trivial #GpuInput so the host's plugin-schema gate has a non-empty,
// base-spliceable schema.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{Class: "verb", Word: "gpu"}},
		describeSchemaFS, "schema")
}
