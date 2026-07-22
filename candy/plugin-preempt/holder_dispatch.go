package preempt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/enginekit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// holder_dispatch.go — FLOOR-SLIM-proper Unit-8's arbiter_host.go/preempt.go MOVE: the
// per-holder lifecycle dispatch (running/stop[+wait]/start, the GPU driver-mode flip, and the
// GPU-CDI reclaim scan) that used to reach BACK to the host over 6 of the 8 ExecutorService.
// HostArbiter seams (running/stop/start/switchMode/ensureCDI/gpuCDI) now runs HERE, IN the
// plugin — none of it is actually K1-blocked (LoadUnified-coupled); each function was reached
// over the host seam only because its ORIGINAL implementation used charly-core-private
// mechanisms (providerRegistry, the connectPluginByWordRef/deployTraitDescent registry calls)
// that have a plugin-side equivalent (sdk.Executor.InvokeProvider, the addr.Vm discriminator
// already carried on the wire type) or are already plugin-importable (sdk/kit, sdk/deploykit,
// sdk/enginekit, sdk/vmshared — including kit.ReadinessProvider, the SAME project-aware
// resolver charly-core's own readiness_config.go injects at init and this compiled-in plugin
// shares via the SAME process, so no new HostBuild seam is needed for the stop-wait gate
// either).
//
// What genuinely STAYS behind HostArbiter: `gather` (gatherPreemptibleHolders, which needs
// LoadUnified) and `resources` (gatherResources, same) — see arbiter.go's newArbiter for the
// two remaining hostGather/hostResources wires.
//
// The ORIGINAL per-holder venue discriminator was `deployTraitDescent(addr.Target).Venue ==
// "ssh"` — a charly-core-PRIVATE registry call (providerRegistry.ResolveKind). The SAME fact is
// already ON THE WIRE: holderAddrFor (host-side, inside the K1-blocked `gather` seam) populates
// addr.Vm ONLY for a vm-venue holder, so every function below dispatches on `addr.Vm != ""`
// instead — pure data, zero registry coupling, byte-identical routing outcome.

// pluginHolderRunning reports whether addr's deployment is currently up — the plugin-side twin
// of the deleted charly/preempt.go's holderRunning (vmIsRunning/podIsRunning folded in).
func pluginHolderRunning(ctx context.Context, exec *sdk.Executor, addr spec.HolderAddr) bool {
	if addr.Vm != "" {
		return vmRunning(ctx, exec, vmDomainName(addr))
	}
	return podRunning(addr.Base, addr.Instance)
}

// pluginHolderStop gracefully stops addr's deployment AND WAITS until it is actually powered off
// (the resource is truly freed) — the folded stop+wait seam (arbiter_host.go's stopAndWait),
// using kit.ReadinessProvider() (the SAME project-aware resolver charly-core injects — shared
// in-process, compiled-in placement) for the wait bound instead of a new HostBuild seam.
func pluginHolderStop(ctx context.Context, exec *sdk.Executor, addr spec.HolderAddr) error {
	var stopErr error
	if addr.Vm != "" {
		stopErr = stopVMPlugin(ctx, exec, vmDomainName(addr), false)
	} else {
		stopErr = deploykit.StopPodService(addr.Base, addr.Instance)
	}
	if stopErr != nil {
		return stopErr
	}
	cfg := kit.ReadinessProvider().StopGate("stop " + addr.Name)
	if vmshared.PollUntil(ctx, cfg, func(context.Context) (bool, float64, error) {
		return !pluginHolderRunning(ctx, exec, addr), 0, nil
	}) != nil {
		return fmt.Errorf("holder %q did not reach a stopped state within the stop grace (resource not freed)", addr.Name)
	}
	return nil
}

// pluginHolderStart starts addr's ALREADY-CONFIGURED deployment — the plugin-side twin of
// holderStart. A DEPARTED holder (no container/quadlet or VM domain left — e.g. a torn-down
// check-bed member) is a no-op success: nothing to restore, so its token frees rather than
// stranding the lease forever.
//
// The VM branch does NOT pre-check existence before starting. An earlier version called
// pluginHolderExists (a "domain-state" RPC) THEN startVMPlugin (a separate "start" RPC) — a
// classic TOCTOU check-then-act: the bed's own sibling teardown step can destroy the domain in
// the WINDOW between the two calls, since restoreHolders (triggered by the taker member's
// removal) runs independently of the holder member's own teardown. Confirmed live: two
// apparently-identical check-preempt-vm-live runs produced DIFFERENT outcomes — one took the
// clean departed-holder no-op, the other hit libvirt's real "domain not found" as a HARD ERROR
// with the lease left stranded — depending on exactly when the race landed. The fix ELIMINATES
// the window instead of synchronizing it (R4: no retry/sleep — there is nothing to synchronize
// once there is only one call): attempt the start UNCONDITIONALLY and fold the
// departed-holder detection into the START RPC's OWN error reply (isDomainNotFoundErr) — a
// domain that no longer exists surfaces the SAME "domain not found" text whether it never
// existed, was already gone before this call, or was destroyed a microsecond before libvirt
// processed the request; there is no longer a separate observation that can go stale.
func pluginHolderStart(ctx context.Context, exec *sdk.Executor, addr spec.HolderAddr) error {
	if addr.Vm != "" {
		err := startVMPlugin(ctx, exec, vmDomainName(addr))
		if err == nil {
			return nil
		}
		if isDomainNotFoundErr(err) {
			fmt.Fprintf(os.Stderr, "preempt: holder %q has departed (no VM domain) — nothing to restore, freeing its lease\n", addr.Name)
			return nil
		}
		return err
	}
	if !pluginHolderExists(addr) {
		fmt.Fprintf(os.Stderr, "preempt: holder %q has departed (no container/quadlet) — nothing to restore, freeing its lease\n", addr.Name)
		return nil
	}
	return deploykit.StartPodService(addr.Base, addr.Instance)
}

// isDomainNotFoundErr reports whether err is candy/plugin-vm's provider.go reply for a missing
// libvirt domain on the start/stop VmOps (`{"error": "domain not found: " + err.Error()}` — a
// lookupDomain miss is a normal, error-free RPC reply, never a transport failure; the "not
// found" signal rides the reply's OWN error text). This is the departed-holder detection folded
// into the act itself, replacing a separate pre-check that raced a concurrent teardown.
func isDomainNotFoundErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "domain not found")
}

// pluginHolderExists reports whether a POD holder's runtime object (container/quadlet) still
// exists — the pod-venue departed-holder guard (see pluginHolderStart's doc comment for why the
// VM venue folds this detection into its start RPC's error instead of a separate pre-check).
func pluginHolderExists(addr spec.HolderAddr) bool {
	if active, _ := kit.QuadletExistsInstance(addr.Base, addr.Instance); active {
		return true
	}
	engine := "podman"
	if rt, err := kit.ResolveRuntime(); err == nil {
		engine = kit.EngineBinary(deploykit.ResolveBoxEngineForDeploy(addr.Base, addr.Instance, rt.RunEngine))
	}
	return exec2Command(engine, "container", "exists", kit.ContainerNameInstance(addr.Base, addr.Instance)) == nil
}

// pluginGpuCDIHolders lists every RUNNING charly-<deploy> pod container that holds the nvidia
// GPU as a CDI device — the reclaim seam, zero registry coupling (pure sdk/kit +
// sdk/enginekit), so it never needed the host seam at all.
func pluginGpuCDIHolders() []spec.HolderAddr {
	rt, err := kit.ResolveRuntime()
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

// devicesHoldNvidiaGPU reports whether a container's inspected device list carries the nvidia
// GPU — the CDI name (`nvidia.com/gpu…`) or a `/dev/nvidia*` node.
func devicesHoldNvidiaGPU(devices []string) bool {
	for _, d := range devices {
		if strings.Contains(d, "nvidia.com/gpu") || strings.HasPrefix(d, "/dev/nvidia") {
			return true
		}
	}
	return false
}

// pluginSwitchMode flips a gpu-backed token's driver mode via InvokeProvider(ClassVerb, "gpu",
// ...) — the SAME generic plugin-to-plugin dispatch spike-proven live for ClassBuilder earlier
// in this program; the class-agnostic InvokeProvider RPC handler treats "verb"/"gpu" identically
// to any other (class, word) pair.
func pluginSwitchMode(ctx context.Context, exec *sdk.Executor, vendor, mode string) (bool, error) {
	r, err := gpuSwitchInvoke(ctx, exec, spec.GpuSwitchInput{Action: spec.GpuSwitchActionMode, Vendor: vendor, Mode: mode})
	if err != nil {
		return false, err
	}
	if r.Error != "" {
		return r.Wedged, fmt.Errorf("%s", r.Error)
	}
	return r.Wedged, nil
}

// pluginEnsureCDI regenerates /etc/cdi/nvidia.yaml as root after a flip to nvidia.
func pluginEnsureCDI(ctx context.Context, exec *sdk.Executor) {
	if _, err := gpuSwitchInvoke(ctx, exec, spec.GpuSwitchInput{Action: spec.GpuSwitchActionEnsureCDI}); err != nil {
		fmt.Fprintf(os.Stderr, "preempt: ensureCDI: %v\n", err)
	}
}

func gpuSwitchInvoke(ctx context.Context, exec *sdk.Executor, in spec.GpuSwitchInput) (spec.GpuSwitchReply, error) {
	params, err := json.Marshal(in)
	if err != nil {
		return spec.GpuSwitchReply{}, err
	}
	out, err := exec.InvokeProvider(ctx, "verb", "gpu", sdk.OpRun, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return spec.GpuSwitchReply{}, err
	}
	var reply spec.GpuSwitchReply
	if len(out) > 0 {
		if uerr := json.Unmarshal(out, &reply); uerr != nil {
			return spec.GpuSwitchReply{}, uerr
		}
	}
	return reply, nil
}

// --- VM dispatch (the InvokeProvider(ClassVerb, "libvirt", ...) twin of charly's deleted
// charly/vm_plugin_client.go invokeVmPlugin/invokeVmPluginEnv) --------------------------------

func vmPluginCandyRef() string {
	return "@" + spec.DefaultProjectRepo + "/candy/plugin-vm"
}

// invokeVmPluginOp InvokeProviders the vm plugin (verb:libvirt) for an internal VM-resolution op
// (domain-state / list-domains) — the S3b canonical-ref fallback (ExtraRef) mirrors
// charly-core's own connectPluginByWordRef(..., vmPluginCandyRef()) scoping so a box/<distro>
// deploy that triggers this arbiter path but vendors candy/plugin-vm nowhere still resolves it.
func invokeVmPluginOp(ctx context.Context, exec *sdk.Executor, vmOp, vmName, uri string) (json.RawMessage, bool) {
	envJSON, err := json.Marshal(spec.VmPluginEnv{VmOp: vmOp, VmName: vmName, URI: uri})
	if err != nil {
		return nil, false
	}
	out, err := exec.InvokeProvider(ctx, "verb", "libvirt", sdk.OpRun, nil, envJSON, sdk.InvokeProviderOpts{ExtraRef: vmPluginCandyRef()})
	if err != nil {
		return nil, false
	}
	return out, true
}

func vmRunning(ctx context.Context, exec *sdk.Executor, name string) bool {
	if raw, ok := invokeVmPluginOp(ctx, exec, "domain-state", name, ""); ok {
		var st struct {
			Running bool `json:"running"`
		}
		if json.Unmarshal(raw, &st) == nil && st.Running {
			return true
		}
	}
	dir, err := vmDirPlugin()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, name, "qemu.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func stopVMPlugin(ctx context.Context, exec *sdk.Executor, name string, force bool) error {
	raw, ok := invokeVmPluginOp2(ctx, exec, spec.VmPluginEnv{VmOp: "stop", VmName: name, Force: force})
	if !ok {
		return fmt.Errorf("VM %s: vm plugin unavailable (go-libvirt is out-of-process)", name)
	}
	if e := vmPluginOpErr(raw); e != "" {
		return fmt.Errorf("stopping VM %s: %s", name, e)
	}
	return nil
}

func startVMPlugin(ctx context.Context, exec *sdk.Executor, name string) error {
	raw, ok := invokeVmPluginOp2(ctx, exec, spec.VmPluginEnv{VmOp: "start", VmName: name})
	if !ok {
		return fmt.Errorf("VM %s: vm plugin unavailable (go-libvirt is out-of-process)", name)
	}
	if e := vmPluginOpErr(raw); e != "" {
		return fmt.Errorf("starting VM %s: %s", name, e)
	}
	return nil
}

func invokeVmPluginOp2(ctx context.Context, exec *sdk.Executor, env spec.VmPluginEnv) (json.RawMessage, bool) {
	envJSON, err := json.Marshal(env)
	if err != nil {
		return nil, false
	}
	out, err := exec.InvokeProvider(ctx, "verb", "libvirt", sdk.OpRun, nil, envJSON, sdk.InvokeProviderOpts{ExtraRef: vmPluginCandyRef()})
	if err != nil {
		return nil, false
	}
	return out, true
}

func vmPluginOpErr(raw json.RawMessage) string {
	var r struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.Error
}

// vmDomainName computes the FULL libvirt domain name ("charly-<identity>") for a vm-venue
// holder/claimant addr. The libvirt domain is keyed by the DEPLOY name (addr.Name — P33: "a
// bed's libvirt domain is keyed by the DEPLOY name, NOT the shared kind:vm entity"), never by
// addr.Vm (the inherited vm TEMPLATE entity, e.g. multiple deploys sharing one `from:`
// template) — a bug the ORIGINAL charly-core preempt.go carried too (it called
// vmName(addr.Vm, addr.Instance), same mistake, just never live-tested until the
// check-preempt-vm-live bed FLOOR-SLIM-proper Unit-8B added). vmshared.VmDomainIdentity
// already folds any instance suffix into addr.Name (e.g. "arch/test" -> "arch-test"), so no
// separate instance parameter is needed.
func vmDomainName(addr spec.HolderAddr) string {
	return "charly-" + vmshared.VmDomainIdentity(addr.Name)
}

func vmDirPlugin() (string, error) {
	return vmshared.VmStateRoot()
}

// podRunning reports whether a pod deployment is up (the quadlet service when one exists, else
// the container's runtime state) — the plugin-side twin of charly/preempt.go's podIsRunning.
func podRunning(base, instance string) bool {
	if active, _ := kit.QuadletExistsInstance(base, instance); active {
		svc := kit.ServiceNameInstance(base, instance)
		out, _ := exec.Command("systemctl", "--user", "is-active", svc).Output()
		return strings.TrimSpace(string(out)) == "active"
	}
	engine := "podman"
	if rt, err := kit.ResolveRuntime(); err == nil {
		engine = kit.EngineBinary(deploykit.ResolveBoxEngineForDeploy(base, instance, rt.RunEngine))
	}
	name := kit.ContainerNameInstance(base, instance)
	out, err := exec.Command(engine, "inspect", "--format", "{{.State.Running}}", name).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// exec2Command runs a container-existence probe, returning nil on exit 0 (mirrors
// exec.Command(...).Run()'s error contract used by holderExists).
func exec2Command(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
