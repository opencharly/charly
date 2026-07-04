package gpu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// switch.go — the GPU driver-MODE switch primitive (cutover C9), the vfio-pci <-> nvidia
// rebind that lets one passthrough-capable NVIDIA card serve EITHER a VM (vfio) OR many
// shared CDI pods (nvidia), one mode at a time. Formerly charly/gpu_driver_switch.go; moved
// here (1B — the driver-switch is GPU logic) and served over verb:gpu's OpRun alongside the
// C11 detection actions. The arbiter's switchMode/ensureCDI host-seams (arbiter_host.go)
// route to these via the core gpu shims (GpuSwitchModeTolerant/EnsureCDIRoot); `charly vm gpu`
// reaches them via the same shims.
//
// THE DEVICE-LOCK HAZARD (unchanged from the RCA, gpu-driver-switch-wedge-rca.md): the nvidia
// `.remove` blocks forever holding the PCI device_lock when a client holds the GPU. THE FIX:
// never sysfs-unbind a busy nvidia — `modprobe -r` is refcount-guarded (EBUSY fast-fail).
// runGPUSwitchScript bounds the whole op so a GSP stall frees charly; a confirmed wedge is
// carried back as spec.ErrGPUSwitchWedged (over the wire: GpuSwitchReply.Wedged) so the
// arbiter poisons the resource until reboot.

const (
	gpuSwitchTimeout   = 90 * time.Second
	gpuSwitchWaitDelay = 5 * time.Second

	// nvidiaInUseMarker is the stable substring switchScriptToVfio prints (and
	// switchGPUDriverMode detects) when `modprobe -r nvidia` returns EBUSY. ONE source for
	// the bash echo + the Go detection so they never drift (R3).
	nvidiaInUseMarker = "nvidia module still in use"
)

// runGPUSwitchScript executes a root sysfs-rebind script under a bounded context so a kernel
// stall can never block charly forever. Package var so tests fake it. A deadline timeout maps
// to spec.ErrGPUSwitchWedged (the only thing that makes a brief rebind run >90s is the
// device_lock wedge / GSP-teardown stall).
var runGPUSwitchScript = func(script string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gpuSwitchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "bash", "-c", script)
	cmd.WaitDelay = gpuSwitchWaitDelay
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, spec.ErrGPUSwitchWedged
	}
	return out, err
}

// vfioDetect is the VFIO detection seam gpuSwitchModeTolerant uses (package var for tests to
// inject an absent/present card without touching sysfs).
var vfioDetect = defaultDetectVFIO

// gpuDisplayDriver reads the live driver bound to a PCI function from sysfs ("" when unbound).
var gpuDisplayDriver = func(addr string) string {
	link, err := os.Readlink("/sys/bus/pci/devices/" + addr + "/driver")
	if err != nil {
		return ""
	}
	if i := strings.LastIndex(link, "/"); i >= 0 {
		return link[i+1:]
	}
	return link
}

// gpuModeFromDriver maps a live driver name to a mode. Anything NOT the nvidia driver
// (vfio-pci, unbound, nouveau, …) is the vfio/default side.
func gpuModeFromDriver(driver string) string {
	if driver == spec.GpuModeNvidia {
		return spec.GpuModeNvidia
	}
	return spec.GpuModeVfio
}

// currentGPUMode reports the live mode of a GPU's DISPLAY function (sysfs, not the cached
// Driver, so it reflects reality after a flip).
func currentGPUMode(gpu spec.VFIOGpu) string {
	return gpuModeFromDriver(gpuDisplayDriver(gpu.Addr))
}

// hostDriverForFunction maps an IOMMU-group member (by PCI class) + target mode to the host
// driver it should bind to.
func hostDriverForFunction(class, mode string) string {
	if mode == spec.GpuModeVfio {
		return spec.HostDriverVfio
	}
	switch {
	case strings.HasPrefix(class, "0x03"): // VGA / 3D / display controller
		return spec.HostDriverDisplay
	case class == "0x0403": // HDMI/DisplayPort audio
		return spec.HostDriverAudio
	default:
		return spec.HostDriverVfio
	}
}

// groupInMode reports whether EVERY function of the GPU's IOMMU group is already bound to the
// driver the target mode wants (the idempotency gate, group-aware).
func groupInMode(gpu spec.VFIOGpu, mode string) bool {
	for _, m := range gpu.GroupMembers {
		if gpuDisplayDriver(m.Addr) != hostDriverForFunction(m.Class, mode) {
			return false
		}
	}
	return true
}

// switchScriptToNvidia builds the group-aware vfio->host rebind. The BIND direction never
// enters the nvidia .remove hazard.
func switchScriptToNvidia(gpu spec.VFIOGpu) string {
	var b strings.Builder
	b.WriteString("set -u\n")
	for _, m := range gpu.GroupMembers {
		target := hostDriverForFunction(m.Class, spec.GpuModeNvidia)
		fmt.Fprintf(&b, "modprobe %s 2>/dev/null || true\n", target)
		fmt.Fprintf(&b, "a=%q; want=%q\n", m.Addr, target)
		b.WriteString("cur=$(readlink /sys/bus/pci/devices/$a/driver 2>/dev/null); cur=${cur##*/}\n")
		b.WriteString("if [ -n \"$cur\" ] && [ \"$cur\" != \"$want\" ]; then echo \"$a\" > /sys/bus/pci/drivers/$cur/unbind 2>/dev/null || true; fi\n")
		b.WriteString("echo \"$want\" > /sys/bus/pci/devices/$a/driver_override\n")
		b.WriteString("echo \"$a\" > /sys/bus/pci/drivers_probe 2>/dev/null || true\n")
	}
	b.WriteString("nvidia-modprobe -c 0 -u 2>/dev/null || true\n")
	fmt.Fprintf(&b, "d=%q\n", gpu.Addr)
	b.WriteString("drv=$(readlink /sys/bus/pci/devices/$d/driver 2>/dev/null); drv=${drv##*/}\n")
	b.WriteString("[ \"$drv\" = nvidia ] || { echo \"switch-to-nvidia FAILED: $d driver=${drv:-unbound}\" >&2; exit 1; }\n")
	return b.String()
}

// switchScriptToVfio builds the group-aware host->vfio rebind via the RDD-proven SAFE detach
// (guarded module unload; NEVER a sysfs-unbind of a busy nvidia).
func switchScriptToVfio(gpu spec.VFIOGpu) string {
	var b strings.Builder
	b.WriteString("set -u\n")
	b.WriteString("systemctl stop nvidia-persistenced 2>/dev/null || true\n")
	b.WriteString("modprobe -r nvidia_drm nvidia_modeset nvidia_uvm nvidia_peermem 2>/dev/null || true\n")
	b.WriteString("if lsmod | grep -q '^nvidia '; then\n")
	b.WriteString("  if ! modprobe -r nvidia 2>/dev/null; then\n")
	fmt.Fprintf(&b, "    echo \"switch-to-vfio REFUSED: %s (a GPU client holds the card) — refusing to force-unbind (would wedge the device_lock)\" >&2\n", nvidiaInUseMarker)
	b.WriteString("    exit 3\n")
	b.WriteString("  fi\n")
	b.WriteString("fi\n")
	b.WriteString("modprobe vfio-pci 2>/dev/null || true\n")
	for _, m := range gpu.GroupMembers {
		fmt.Fprintf(&b, "a=%q\n", m.Addr)
		b.WriteString("cur=$(readlink /sys/bus/pci/devices/$a/driver 2>/dev/null); cur=${cur##*/}\n")
		b.WriteString("if [ -n \"$cur\" ] && [ \"$cur\" != vfio-pci ]; then echo \"$a\" > /sys/bus/pci/drivers/$cur/unbind 2>/dev/null || true; fi\n")
		b.WriteString("echo vfio-pci > /sys/bus/pci/devices/$a/driver_override\n")
		b.WriteString("echo \"$a\" > /sys/bus/pci/drivers_probe 2>/dev/null || true\n")
		b.WriteString("echo \"\" > /sys/bus/pci/devices/$a/driver_override\n")
	}
	b.WriteString("rc=0\n")
	for _, m := range gpu.GroupMembers {
		fmt.Fprintf(&b, "a=%q\n", m.Addr)
		b.WriteString("drv=$(readlink /sys/bus/pci/devices/$a/driver 2>/dev/null); drv=${drv##*/}\n")
		b.WriteString("if [ \"$drv\" != vfio-pci ]; then\n")
		b.WriteString("  if grep -lqs -e nv_pci_remove -e os_delay /proc/*/stack 2>/dev/null; then echo \"switch-to-vfio WEDGED: $a driver=${drv:-unbound}; nv_pci_remove in D-state — host reboot required\" >&2; exit 4; fi\n")
		b.WriteString("  echo \"switch-to-vfio FAILED: $a driver=${drv:-unbound}\" >&2; rc=1\n")
		b.WriteString("fi\n")
	}
	b.WriteString("exit $rc\n")
	return b.String()
}

// switchScriptFor builds the rebind script for a target mode (the DRY-RUN plan is exactly
// these bytes, never executed).
func switchScriptFor(gpu spec.VFIOGpu, mode string) (string, error) {
	switch mode {
	case spec.GpuModeNvidia:
		return switchScriptToNvidia(gpu), nil
	case spec.GpuModeVfio:
		return switchScriptToVfio(gpu), nil
	default:
		return "", fmt.Errorf("unknown GPU mode %q (want %q or %q)", mode, spec.GpuModeVfio, spec.GpuModeNvidia)
	}
}

// switchGPUDriverMode rebinds the GPU's WHOLE IOMMU group to the target mode. Idempotent (a
// no-op, no sudo, when already in mode). Returns wedged=true (+ spec.ErrGPUSwitchWedged) on a
// device_lock wedge so the caller can carry the wedge over the wire and poison the resource.
func switchGPUDriverMode(gpu spec.VFIOGpu, mode string) (wedged bool, err error) {
	if groupInMode(gpu, mode) {
		return false, nil
	}
	script, serr := switchScriptFor(gpu, mode)
	if serr != nil {
		return false, serr
	}
	out, runErr := runGPUSwitchScript(script)
	if runErr == nil {
		return false, nil
	}
	if errors.Is(runErr, spec.ErrGPUSwitchWedged) || strings.Contains(string(out), "WEDGED") {
		// Carry the wedge as a bool over the wire (GpuSwitchReply.Wedged); the raw detail
		// rides reply.Error and the CORE gpu shim re-wraps spec.ErrGPUSwitchWedged so callers
		// keep matching with errors.Is (the sentinel can't cross the process boundary).
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = "device_lock wedge (switch deadline exceeded)"
		}
		return true, errors.New(detail)
	}
	if mode == spec.GpuModeVfio && strings.Contains(string(out), nvidiaInUseMarker) {
		return false, nvidiaInUseRefusal(discoverNvidiaHolders())
	}
	return false, fmt.Errorf("switching GPU %s to %s mode: %w\n%s", gpu.Addr, mode, runErr, strings.TrimSpace(string(out)))
}

// NvidiaHolder is one process holding an /dev/nvidia* device open.
type NvidiaHolder struct {
	PID  int
	Comm string
}

var discoverNvidiaHolders = defaultDiscoverNvidiaHolders

func defaultDiscoverNvidiaHolders() []NvidiaHolder {
	fdDirs, _ := filepath.Glob("/proc/[0-9]*/fd")
	var holders []NvidiaHolder
	for _, fdDir := range fdDirs {
		pid, err := strconv.Atoi(filepath.Base(filepath.Dir(fdDir)))
		if err != nil {
			continue
		}
		fds, _ := os.ReadDir(fdDir)
		for _, fd := range fds {
			target, lerr := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if lerr == nil && strings.HasPrefix(target, "/dev/nvidia") {
				holders = append(holders, NvidiaHolder{PID: pid, Comm: procComm(pid)})
				break
			}
		}
	}
	sort.Slice(holders, func(i, j int) bool { return holders[i].PID < holders[j].PID })
	return holders
}

func procComm(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(b))
}

// formatNvidiaHolders renders the holder list into the actionable clause of a vfio-switch
// refusal. Pure + unit-tested.
func formatNvidiaHolders(holders []NvidiaHolder) string {
	if len(holders) == 0 {
		return "an external GPU client (the holding process could not be identified)"
	}
	parts := make([]string, 0, len(holders))
	for _, h := range holders {
		parts = append(parts, fmt.Sprintf("%s (pid %d)", h.Comm, h.PID))
	}
	return "external process(es): " + strings.Join(parts, ", ")
}

func nvidiaInUseRefusal(holders []NvidiaHolder) error {
	return fmt.Errorf("switch-to-vfio REFUSED: nvidia still held by %s. charly auto-preempts its own GPU pods; close these external GPU clients and retry (refusing to force-unbind — would wedge the device_lock)", formatNvidiaHolders(holders))
}

// ensureCDIRoot (re)generates the nvidia CDI spec at /etc/cdi/nvidia.yaml as ROOT (the
// rootless user cannot write /etc/cdi). Best-effort; no-op when nvidia-ctk is absent.
func ensureCDIRoot() {
	if _, err := exec.LookPath("nvidia-ctk"); err != nil {
		return
	}
	script := "set -e\nmkdir -p /etc/cdi\nnvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml\n"
	if out, err := runGPUSwitchScript(script); err != nil {
		fmt.Fprintf(os.Stderr, "gpu: CDI spec generation failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
	}
}

// gpuWedgeDetected runs a read-only root probe for any task stuck in the nvidia `.remove`
// path (a D-state task whose kernel stack hits nv_pci_remove/os_delay). Read-only.
func gpuWedgeDetected() bool {
	out, _ := runGPUSwitchScript("for p in $(ps -eo pid,stat | awk '$2 ~ /D/{print $1}'); do " +
		"grep -qs -e nv_pci_remove -e os_delay /proc/$p/stack 2>/dev/null && { echo WEDGED; exit 0; }; done; echo CLEAN\n")
	return strings.Contains(string(out), "WEDGED")
}

// gpuSwitchModeTolerant detects the GPU matching vendor and flips its WHOLE IOMMU group to
// mode — TOLERANT of an absent card (the arbiter's switchMode seam, used both directions, so
// a claim stays portable across GPU and no-GPU hosts). Card ABSENT → skip with a note, NO
// error. Returns wedged so the arbiter can poison over the wire.
func gpuSwitchModeTolerant(vendor, mode string) (wedged bool, err error) {
	gpu, found := spec.SelectGPUByVendor(vfioDetect(nil), vendor)
	if !found {
		fmt.Fprintf(os.Stderr, "preempt: no GPU matching vendor %s on this host; skipping %s-mode flip (claim stays portable)\n", spec.NormalizePCIVendor(vendor), mode)
		return false, nil
	}
	return switchGPUDriverMode(gpu, mode)
}

// examplePlanGpu is the synthetic card the DRY-RUN switch-plan uses when the caller supplies
// no real GPU — a documented display+HDMI-audio IOMMU group so the plan is deterministic and
// completely hardware-free (the C9 cred/hw-free dispatch proof).
func examplePlanGpu() spec.VFIOGpu {
	disp := spec.VFIOPCIDevice{Addr: "0000:01:00.0", VendorID: spec.NvidiaVendorID, Class: "0x0300", ClassLabel: "VGA controller"}
	aud := spec.VFIOPCIDevice{Addr: "0000:01:00.1", VendorID: spec.NvidiaVendorID, Class: "0x0403", ClassLabel: "Audio device"}
	return spec.VFIOGpu{VFIOPCIDevice: disp, GroupMembers: []spec.VFIOPCIDevice{disp, aud}}
}

// switchPlan returns the EXACT rebind commands for the target mode WITHOUT touching sysfs —
// the DRY-RUN dispatch proof. gpu==nil synthesizes examplePlanGpu so the plan is available on
// a GPU-less host. Never calls runGPUSwitchScript (build-only, hardware-free).
func switchPlan(gpu *spec.VFIOGpu, mode string) ([]string, error) {
	g := examplePlanGpu()
	if gpu != nil {
		g = *gpu
	}
	if mode == "" {
		mode = spec.GpuModeVfio
	}
	script, err := switchScriptFor(g, mode)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, ln := range strings.Split(script, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			lines = append(lines, s)
		}
	}
	return lines, nil
}

// invokeSwitch handles the C9 DRIVER-SWITCH actions on verb:gpu's OpRun (main.go routes here
// after peeking the action). Decodes spec.GpuSwitchInput, runs the switch primitive, and
// returns spec.GpuSwitchReply. An op failure rides reply.Error (wedge → reply.Wedged); the
// RPC itself succeeds.
func invokeSwitch(params []byte) (*pb.InvokeReply, error) {
	var in spec.GpuSwitchInput
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("gpu: decode switch input: %w", err)
	}
	var reply spec.GpuSwitchReply
	switch in.Action {
	case spec.GpuSwitchActionMode:
		var (
			wedged bool
			err    error
		)
		if in.Gpu != nil {
			wedged, err = switchGPUDriverMode(*in.Gpu, in.Mode) // exact card (`charly vm gpu mode`)
		} else {
			wedged, err = gpuSwitchModeTolerant(in.Vendor, in.Mode) // vendor-select, tolerant (the arbiter seam)
		}
		reply.Wedged, reply.Error = wedged, errStr(err)
	case spec.GpuSwitchActionEnsureCDI:
		ensureCDIRoot()
	case spec.GpuSwitchActionWedgeDetected:
		reply.Bool = gpuWedgeDetected()
	case spec.GpuSwitchActionGroupInMode:
		if in.Gpu != nil {
			reply.Bool = groupInMode(*in.Gpu, in.Mode)
		}
	case spec.GpuSwitchActionCurrentMode:
		if in.Gpu != nil {
			reply.Str = currentGPUMode(*in.Gpu)
		}
	case spec.GpuSwitchActionDisplayDriver:
		reply.Str = gpuDisplayDriver(in.Addr)
	case spec.GpuSwitchActionPlan:
		plan, err := switchPlan(in.Gpu, in.Mode)
		reply.Plan, reply.Error = plan, errStr(err)
	default:
		return nil, fmt.Errorf("gpu: unknown switch action %q", in.Action)
	}
	out, err := json.Marshal(reply)
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

func errStr(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
