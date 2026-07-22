package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// preempt.go — the HOST side of the resource arbiter after cutover C9.
//
// The arbiter LOGIC (the 1225-LOC ResourceArbiter: AcquireExclusive/AcquireShared/
// ReleaseClaimant/stopHolders/restoreHolders/reconcileStranded/the lease ledger/poison/
// mode-math) MOVED into the COMPILED-IN candy/plugin-preempt (verb:arbiter). What stays here:
//
//   1. The in-core PROXY (arbiterProxy + Lease + the acquire*/release* shims) — re-verified at
//      Cutover B unit 6b (the previous consumer list here — check_bed_run.go/start.go/vm.go/
//      commands.go/vm_gpu_cmd.go — was STALE; none of those files call these shims anymore).
//      The REAL, current callers are host_build_check_bed.go's bed arbiter lease
//      (acquireResourceForClaimant), and — permanently, per the B-1 unit-2 IOU #4 ruling —
//      arbiter_bracket.go's (S3b — was substrate_lifecycle_grpc.go before the deploy-dispatch
//      cluster moved) + host_build_pod_lifecycle_dispatch.go's arbiter-release
//      brackets, which stay core-side BY DESIGN (gated on host-process CHARLY_PREEMPT_LEASE
//      env state a placement-agnostic plugin cannot own). Because those two release call sites
//      are permanent, the FULL proxy (not just a thin slice) stays core: each proxy method
//      resolves verb:arbiter and Invokes it with an action-tagged spec.ArbiterInvokeInput (the
//      generic core→verb registry bridge — core is not a plugin, so it cannot call InvokeProvider;
//      the externalized command:preempt CLI reaches the arbiter over InvokeProvider instead).
//   2. The 7 arbiter HOST-SEAM helper impls (gatherPreemptibleHolders / holderRunning /
//      holderStop / holderStart / gatherResources / holderAddrFor / lookupVMClaimant +
//      waitStoppedHost) the arbiter calls back for mid-logic via ExecutorService.HostArbiter
//      (arbiter_host.go delegates here). These read the project config + drive the VM/pod
//      lifecycle — host dependencies that cannot cross into the plugin module (two of them,
//      gatherDeployNodes/gatherResources, also call LoadUnified directly — a second,
//      independent K1-permanent reason they stay core, per R-E2).
//
// Crash-safety, the lease ledger, poison markers, liveness (owner PID/start), and the
// stop/flip/save sequencing all live in the plugin now; the host only supplies the config +
// lifecycle + GPU-flip dependencies over the reverse channel.

// holderAddr is spec.HolderAddr — the self-contained deployment address the host seams act on.
type holderAddr = spec.HolderAddr

// envPreemptLeaseHeld is set by the OUTERMOST claim-bringing `charly` invocation (a check-bed
// run, or a standalone `charly vm create`/`charly start`) so the nested `charly` subprocesses it
// spawns do NOT independently acquire/release the lease — the owner manages it. Managed by the
// in-core shims (the arbiter plugin never sees the env).
const envPreemptLeaseHeld = "CHARLY_PREEMPT_LEASE"

// --- the in-core arbiter PROXY (dispatches to the compiled-in verb:arbiter plugin) ----------

// arbiterProxy is the in-core handle newResourceArbiter() returns to its current 3 callers
// (host_build_check_bed.go, arbiter_bracket.go — S3b, was substrate_lifecycle_grpc.go before
// the deploy-dispatch cluster moved — host_build_pod_lifecycle_dispatch.go
// — re-verified at Cutover B unit 6b). Its methods dispatch to the compiled-in
// candy/plugin-preempt (verb:arbiter) over an in-proc reverse channel — the arbiter runs there
// and calls back for its host seams.
type arbiterProxy struct{}

func newResourceArbiter() *arbiterProxy { return &arbiterProxy{} }

// arbiterInvoke resolves verb:arbiter and Invokes it with an action-tagged input, threading the
// IN-PROC reverse channel onto the ctx so the plugin's Invoke reaches its host seams over HostArbiter
// (now an always-served generic seam — plugin_executor_reverse.go) — the SAME dispatchBuild
// in-proc-executor pattern (build.go). Infra failures (no plugin, marshal, invoke) are returned as a
// Go error; a per-action OP failure rides reply.Error. This is the generic core→verb registry bridge
// the core lease-lifecycle callers use (core is not a plugin, so it cannot call InvokeProvider).
func arbiterInvoke(in spec.ArbiterInvokeInput) (spec.ArbiterInvokeReply, error) {
	prov, ok := providerRegistry.resolve(ClassVerb, "arbiter")
	if !ok {
		return spec.ArbiterInvokeReply{}, fmt.Errorf("resource arbiter (verb:arbiter) not registered — charly built without candy/plugin-preempt")
	}
	ctx := sdk.ContextWithExecutor(context.Background(),
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	reply, err := invokeTyped[spec.ArbiterInvokeInput, spec.ArbiterInvokeReply](ctx, prov, "arbiter", OpRun, in)
	if err != nil {
		return spec.ArbiterInvokeReply{}, fmt.Errorf("arbiter %s: %w", in.Action, err)
	}
	return reply, nil
}

// ReleaseClaimant restores the holders a claimant's lease stopped + removes the lease.
func (a *arbiterProxy) ReleaseClaimant(claimant string, success bool) error {
	r, err := arbiterInvoke(spec.ArbiterInvokeInput{Action: spec.ArbiterActionRelease, Claimant: claimant, Success: success})
	if err != nil {
		return err
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return nil
}

// Lease is the handle returned by the acquire shims. Release()/ReleaseFailed() dispatch the
// release through the arbiter proxy. A zero/no-op Lease (nothing claimed) is safe to Release.
type Lease struct {
	claimant string
	active   bool
}

// Release restores preempted holders assuming the claim succeeded.
func (l *Lease) Release() error {
	if l == nil || !l.active {
		return nil
	}
	return newResourceArbiter().ReleaseClaimant(l.claimant, true)
}

// ReleaseFailed applies the restore policy for a FAILED claim (on-success holders stay stopped).
func (l *Lease) ReleaseFailed() error {
	if l == nil || !l.active {
		return nil
	}
	return newResourceArbiter().ReleaseClaimant(l.claimant, false)
}

// --- the acquire/release shims (compute tokens/addr from the node, then dispatch) -----------

// acquireExclusiveForClaimant acquires (or reuses) an exclusive-resource lease for a claimant
// that declares requires_exclusive — UNLESS an outer orchestrator already owns one
// (envPreemptLeaseHeld). On a real acquire it marks the env so nested `charly` subprocesses
// skip re-acquiring. A no-op lease is safe to Release.
func acquireExclusiveForClaimant(claimant string, node spec.BundleNode, transient bool) (*Lease, error) {
	if len(node.RequiredExclusive()) == 0 {
		return &Lease{}, nil
	}
	if os.Getenv(envPreemptLeaseHeld) != "" {
		return &Lease{}, nil
	}
	return acquireDispatch(spec.ArbiterActionAcquireExclusive, claimant, dedupeNonEmpty(node.RequiredExclusive()), node, transient)
}

// acquireSharedForClaimant acquires (or reuses) a SHARED refcounted lease for a pod/bed that
// declares requires_shared. Mirrors acquireExclusiveForClaimant.
func acquireSharedForClaimant(claimant string, node spec.BundleNode, transient bool) (*Lease, error) {
	if len(node.RequiredShared()) == 0 {
		return &Lease{}, nil
	}
	if os.Getenv(envPreemptLeaseHeld) != "" {
		return &Lease{}, nil
	}
	return acquireDispatch(spec.ArbiterActionAcquireShared, claimant, dedupeNonEmpty(node.RequiredShared()), node, transient)
}

// acquireDispatch is the shared acquire leg (R3): it Invokes verb:arbiter with the pre-computed
// tokens + claim address, and on an active lease marks envPreemptLeaseHeld so nested
// subprocesses skip re-acquiring.
func acquireDispatch(action, claimant string, tokens []string, node spec.BundleNode, transient bool) (*Lease, error) {
	r, err := arbiterInvoke(spec.ArbiterInvokeInput{
		Action:    action,
		Claimant:  claimant,
		Tokens:    tokens,
		ClaimAddr: holderAddrFor(claimant, node),
		Transient: transient,
	})
	if err != nil {
		return nil, err
	}
	if r.Error != "" {
		return nil, errors.New(r.Error)
	}
	if r.Active {
		_ = os.Setenv(envPreemptLeaseHeld, claimant)
	}
	return &Lease{claimant: claimant, active: r.Active}, nil
}

// acquireResourceForClaimant acquires the appropriate lease for a claimant: EXCLUSIVE when it
// declares requires_exclusive, SHARED when it declares requires_shared, a no-op when it claims
// nothing. The single entry point for the start + check-bed paths (R3). A node that USES the
// nvidia GPU but declared NO explicit claim is auto-promoted to a SHARED claimant of the gpu
// token here (withImpliedGPUShared) — so EVERY GPU-consuming deployment becomes a tracked,
// preemptable shared claimant with no per-deploy config.
func acquireResourceForClaimant(claimant string, node spec.BundleNode, transient bool) (*Lease, error) {
	node = withImpliedGPUShared(node)
	if len(node.RequiredExclusive()) > 0 {
		return acquireExclusiveForClaimant(claimant, node, transient)
	}
	if len(node.RequiredShared()) > 0 {
		return acquireSharedForClaimant(claimant, node, transient)
	}
	return &Lease{}, nil
}

// releaseResourceClaim releases a persistent claimant's lease on teardown (charly vm
// stop/destroy, charly stop, charly remove) — kind-agnostic, best-effort, a no-op when the
// claimant holds no lease, skipped when an outer orchestrator owns the lease.
func releaseResourceClaim(claimant string) {
	if os.Getenv(envPreemptLeaseHeld) != "" {
		return
	}
	if err := newResourceArbiter().ReleaseClaimant(claimant, true); err != nil {
		fmt.Fprintf(os.Stderr, "preempt: %v\n", err)
	}
}

// --- the arbiter HOST-SEAM impls (the arbiter calls these back over HostArbiter) ------------

// gatherDeployNodes returns every deploy node visible to the current invocation: the current
// project's deploy map (committed charly.yml, includes folded check beds) as the BASE, with the
// operator's per-host ~/.config/charly/charly.yml overlay merged ON TOP (the overlay WINS on a
// name clash — it carries local-only `preemptible:`, a PER-HOST decision). Keyed by deploy name.
func gatherDeployNodes() map[string]spec.BundleNode {
	out := map[string]spec.BundleNode{}
	if uf, ok, err := LoadUnified("."); err == nil && ok && uf != nil {
		maps.Copy(out, uf.Bundle)
	}
	if dc := deploykit.LoadDeployConfigForRead("charly preempt"); dc != nil {
		for name, node := range dc.Bundle {
			out[name] = deploykit.MergeBundleNode(out[name], node)
		}
	}
	return out
}

// gatherPreemptibleHolders is gatherDeployNodes filtered to the preemptible holders (the
// candidate set the arbiter may stop).
func gatherPreemptibleHolders() map[string]spec.BundleNode {
	out := map[string]spec.BundleNode{}
	for name, node := range gatherDeployNodes() {
		if node.IsPreemptible() {
			out[name] = node
		}
	}
	return out
}

// lookupVMClaimant finds a deploy/check node that targets the given kind:vm entity and declares
// requires_exclusive — the claimant a standalone `charly vm create/stop/destroy <entity>`
// acquires/releases an exclusive lease for. ok=false when none exists.
func lookupVMClaimant(vmEntity string) (string, spec.BundleNode, bool) {
	for name, node := range gatherDeployNodes() {
		if deployTraitDescent(node.Target).Venue == "ssh" && node.From == vmEntity && len(node.RequiredExclusive()) > 0 { // vm (ssh venue)
			return name, node, true
		}
	}
	return "", spec.BundleNode{}, false
}

func holderAddrFor(name string, node spec.BundleNode) holderAddr {
	base, instance := deploykit.ParseDeployKey(name)
	target := node.Target
	if target == "" {
		target = "pod"
	}
	addr := holderAddr{Name: name, Target: target, Base: base, Instance: instance}
	if deployTraitDescent(target).Venue == "ssh" { // vm (ssh venue)
		addr.Vm = node.From
		if addr.Vm == "" {
			addr.Vm = base
		}
	}
	return addr
}

func holderRunning(addr holderAddr) bool {
	if deployTraitDescent(addr.Target).Venue == "ssh" { // vm (ssh venue)
		return vmIsRunning(vmName(addr.Vm, addr.Instance))
	}
	return podIsRunning(addr.Base, addr.Instance)
}

func holderStop(addr holderAddr) error {
	if deployTraitDescent(addr.Target).Venue == "ssh" { // vm (ssh venue)
		return stopVM(addr.Vm, addr.Instance, false)
	}
	return deploykit.StopPodService(addr.Base, addr.Instance)
}

func holderStart(addr holderAddr) error {
	// A DEPARTED holder — its container/quadlet or VM domain removed entirely (e.g. a
	// torn-down check-bed member) — cannot be and need not be restored. Treating the
	// missing runtime object as a hard start error would make restoreHolders fail and
	// strand the lease FOREVER (no CLI could clear it — `charly preempt restore` would
	// keep failing to restart a holder that no longer exists). A departed holder is a
	// no-op success: nothing to restore, so its token frees.
	if !holderExists(addr) {
		fmt.Fprintf(os.Stderr, "preempt: holder %q has departed (no container/quadlet or VM domain) — nothing to restore, freeing its lease\n", addr.Name)
		return nil
	}
	if deployTraitDescent(addr.Target).Venue == "ssh" { // vm (ssh venue)
		return startVM(addr.Vm, addr.Instance)
	}
	return deploykit.StartPodService(addr.Base, addr.Instance)
}

// holderExists reports whether the holder's runtime object still exists — a
// container/quadlet (running or stopped) for a pod holder, or a defined libvirt
// domain for a vm holder. Distinguishes a stopped-but-present holder (restore it)
// from a departed one (free its lease). See holderStart.
func holderExists(addr holderAddr) bool {
	if deployTraitDescent(addr.Target).Venue == "ssh" { // vm (ssh venue)
		if _, ok := invokeVmPlugin("domain-state", vmName(addr.Vm, addr.Instance), ""); ok {
			return true
		}
		if dir, err := vmDir(); err == nil {
			if _, statErr := os.Stat(filepath.Join(dir, vmName(addr.Vm, addr.Instance))); statErr == nil {
				return true
			}
		}
		return false
	}
	if active, _ := kit.QuadletExistsInstance(addr.Base, addr.Instance); active {
		return true
	}
	engine := "podman"
	if rt, err := kit.ResolveRuntime(); err == nil {
		engine = kit.EngineBinary(deploykit.ResolveBoxEngineForDeploy(addr.Base, addr.Instance, rt.RunEngine))
	}
	return exec.Command(engine, "container", "exists", kit.ContainerNameInstance(addr.Base, addr.Instance)).Run() == nil
}

// waitStoppedHost polls until the holder is no longer running (its resource is released), via
// the readiness StopGate + pollUntil (cap-only at the config StopGrace). Returns immediately
// when already down. The folded wait leg of the `stop` host seam — the readiness machinery
// stays host-side, never crossing into the plugin.
func waitStoppedHost(addr holderAddr) bool {
	cfg := loadedReadiness().StopGate("stop " + addr.Name)
	return pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		return !holderRunning(addr), 0, nil
	}) == nil
}

// vmIsRunning reports whether the named domain is running (libvirt state via the vm plugin,
// then the qemu pidfile).
func vmIsRunning(name string) bool {
	if raw, ok := invokeVmPlugin("domain-state", name, ""); ok {
		var st struct {
			Running bool `json:"running"`
		}
		if json.Unmarshal(raw, &st) == nil && st.Running {
			return true
		}
	}
	dir, err := vmDir()
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

// podIsRunning reports whether a pod deployment is up (the quadlet service when one exists,
// else the container's runtime state).
func podIsRunning(base, instance string) bool {
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

// gatherResources loads the token -> ResourceDef map (the gpu selector that drives the mode
// flip) from the project charly.yml. nil when none / unreadable.
func gatherResources() map[string]*ResolvedResource {
	if uf, ok, err := LoadUnified("."); err == nil && ok && uf != nil {
		return uf.resolveResources()
	}
	return nil
}

// --- small host-side set helpers -----------------------------------------------------------

// dedupeNonEmpty trims + dedups a token list (the acquire shim computes the claimant's tokens).
func dedupeNonEmpty(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// sortedHolderKeys returns the sorted keys of a holder map (the gather projection iterates
// deterministically).
func sortedHolderKeys(m map[string]spec.BundleNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// intersect returns the sorted set intersection of a and b — used by validate_preempt.go's
// requires_exclusive/requires_shared overlap check (the arbiter's own copy travels with it in
// candy/plugin-preempt, its separate module).
func intersect(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range b {
		if set[s] && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
