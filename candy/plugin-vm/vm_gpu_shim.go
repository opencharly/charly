package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// vm_gpu_shim.go — the command:vm plugin's bridge to verb:gpu, the plugin-side twin of core's
// gpu_shim.go. The GPU/VFIO detection + driver-switch LOGIC lives in candy/plugin-gpu (verb:gpu);
// the moved `charly vm gpu` command + gpu_allocate's auto-allocation reach it over the reverse
// channel (InvokeProvider) instead of core's hostInvokeOr/providerRegistry.resolve. The consts/
// value helpers/result types alias package spec (the ONE copy). Best-effort, never-crash: a missing
// reverse channel or verb:gpu degrades to a zero reply + a loud stderr note (core's contract).
//
// NOTE: DetectVFIO omits the core-only PCIClassLabels data table (devices.go) — the non-GPU vm beds
// never invoke a GPU claim, so this compiles + no-ops correctly there; GPU-passthrough correctness is
// gated by check-gpu-local, where the table threading is refined (a follow-up if that bed needs it).

const (
	gpuModeVfio       = spec.GpuModeVfio
	gpuModeNvidia     = spec.GpuModeNvidia
	nvidiaVendorID    = spec.NvidiaVendorID
	hostDriverDisplay = spec.HostDriverDisplay
	hostDriverVfio    = spec.HostDriverVfio
)

var errGPUSwitchWedged = spec.ErrGPUSwitchWedged

var (
	normalizePCIVendor = spec.NormalizePCIVendor
	selectGPUByVendor  = spec.SelectGPUByVendor
)

// gpuProbeReply resolves verb:gpu over the reverse channel and Invokes it with the probe action.
func gpuProbeReply(in spec.GpuProbeInput) spec.GpuProbeReply {
	var out spec.GpuProbeReply
	if cmdExec == nil {
		fmt.Fprintln(os.Stderr, "warning: gpu probe: no host reverse channel (command not compiled-in?)")
		return out
	}
	inJSON, err := json.Marshal(in)
	if err != nil {
		return out
	}
	res, err := cmdExec.InvokeProvider(cmdCtx, "verb", "gpu", sdk.OpRun, inJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: gpu probe %s: %v\n", in.Action, err)
		return out
	}
	_ = json.Unmarshal(res, &out)
	return out
}

// DetectVFIO probes the host for IOMMU readiness + passthrough-capable GPUs (var for testability).
var DetectVFIO = func() VFIOReport {
	reply := gpuProbeReply(spec.GpuProbeInput{Action: "detect-vfio"})
	if reply.Vfio == nil {
		return VFIOReport{}
	}
	return *reply.Vfio
}

// MemlockLimitBytes returns the process RLIMIT_MEMLOCK (soft, hard) — VFIO pins guest RAM.
func MemlockLimitBytes() (soft, hard uint64) { //nolint:unparam // rlimit-pair API completeness
	reply := gpuProbeReply(spec.GpuProbeInput{Action: "memlock"})
	return reply.MemlockSoft, reply.MemlockHard
}

// VfioGroupAccessible reports whether the user can open /dev/vfio/<group>. group<0 → no IOMMU group.
func VfioGroupAccessible(group int) bool {
	if group < 0 {
		return false
	}
	return gpuProbeReply(spec.GpuProbeInput{Action: "vfio-group-accessible", Group: group}).Bool
}

// gpuSwitchReply resolves verb:gpu and Invokes it with a driver-switch action (error rides reply.Error).
func gpuSwitchReply(in spec.GpuSwitchInput) spec.GpuSwitchReply {
	var out spec.GpuSwitchReply
	if cmdExec == nil {
		fmt.Fprintln(os.Stderr, "warning: gpu switch: no host reverse channel (command not compiled-in?)")
		return out
	}
	inJSON, err := json.Marshal(in)
	if err != nil {
		return out
	}
	res, err := cmdExec.InvokeProvider(cmdCtx, "verb", "gpu", sdk.OpRun, inJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: gpu switch %s: %v\n", in.Action, err)
		return spec.GpuSwitchReply{Error: err.Error()}
	}
	_ = json.Unmarshal(res, &out)
	return out
}

// switchReplyErr maps a GpuSwitchReply's op result to an error (wedge re-wraps errGPUSwitchWedged).
func switchReplyErr(r spec.GpuSwitchReply) error {
	if r.Wedged {
		if d := strings.TrimSpace(r.Error); d != "" {
			return fmt.Errorf("%w\n%s", errGPUSwitchWedged, d)
		}
		return errGPUSwitchWedged
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

func switchGPUDriverMode(gpu VFIOGpu, mode string) error {
	return switchReplyErr(gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionMode, Gpu: &gpu, Mode: mode}))
}

func ensureCDIRoot() { gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionEnsureCDI}) }

func gpuWedgeDetected() bool {
	return gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionWedgeDetected}).Bool
}

func groupInMode(gpu VFIOGpu, mode string) bool {
	return gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionGroupInMode, Gpu: &gpu, Mode: mode}).Bool
}

func currentGPUMode(gpu VFIOGpu) string {
	return gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionCurrentMode, Gpu: &gpu}).Str
}

func gpuDisplayDriver(addr string) string {
	return gpuSwitchReply(spec.GpuSwitchInput{Action: spec.GpuSwitchActionDisplayDriver, Addr: addr}).Str
}

func gpuSwitchPlan(gpu *VFIOGpu, mode string) ([]string, error) {
	in := spec.GpuSwitchInput{Action: spec.GpuSwitchActionPlan, Mode: mode}
	if gpu != nil {
		in.Gpu = gpu
	}
	r := gpuSwitchReply(in)
	return r.Plan, switchReplyErr(r)
}
