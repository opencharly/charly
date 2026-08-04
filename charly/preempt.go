package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
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
//      The check-bed session's own arbiter lease dissolved entirely (#55 W3 B2-full):
//      candy/plugin-check/bed_session.go now calls verb:arbiter DIRECTLY via InvokeProvider (the
//      SAME vm_arbiter_shim.go precedent noted below), never through this in-core proxy. The
//      REAL, current callers are — permanently, per the B-1 unit-2 IOU #4 ruling —
//      host_build_arbiter_bracket.go's (the F10 HostBuild seam candy/plugin-bundle's
//      arbiter-bracket-acquire/-release dispatch calls, FLOOR-SLIM-proper Unit-8; was
//      arbiter_bracket.go core-resident before that move) + host_build_pod_lifecycle_dispatch.go's
//      arbiter-release brackets, which stay core-side BY DESIGN (gated on host-process
//      CHARLY_PREEMPT_LEASE env state a placement-agnostic plugin cannot own). Because those two
//      release call sites are permanent, the FULL proxy (not just a thin slice) stays core: each
//      proxy method resolves verb:arbiter and Invokes it with an action-tagged
//      spec.ArbiterInvokeInput (the generic core→verb registry bridge — core is not a plugin, so
//      it cannot call InvokeProvider; the externalized command:preempt CLI reaches the arbiter
//      over InvokeProvider instead).
//   2. gatherResources — REWIRED off LoadUnified(".") onto the SAME generic
//      InvokeProvider("build","project") envelope every other resolved-project consumer uses
//      (K-wave W3a A2), retiring its K1/LoadUnified coupling now rather than waiting for #12. Its
//      sole remaining caller is gpu_allocate.go's bedGPUPrereqMissing, reached now via the narrow
//      host_build_check_bed_gpu_prereq.go seam (#55 W3 B2-full — GPU host-DETECTION is the
//      project's operator-dropped exception, the ONE piece of check-bed's former session that
//      stayed core); gpu_imply.go's former call moved to candy/plugin-preempt (K-wave W3a A2) and
//      no longer needs this function. The arbiter itself doesn't reach it either:
//      candy/plugin-preempt reads resources off its OWN InvokeProvider("build","project")
//      envelope (rp.Resources) directly.
//
//   K1-unblock wave 1 retired the former bespoke arbiter reverse-RPC channel entirely (the
//   "2 genuinely K1-blocked seams" it carried — gather/resources): candy/plugin-preempt now
//   reads its deploy tree + resources off InvokeProvider("build","project") and does its own
//   holder-filtering/VM-claimant-lookup in-plugin via the portable sdk/deploykit helpers
//   (FilterPreemptibleHolders/FindVMClaimant/HolderAddrFor/MergedDeployTree), the SAME functions
//   host_build_config_resolve.go's VM-claimant lookup now shares (R3). The former host handler,
//   its reverse-RPC method + request/reply proto messages, and the local
//   gatherDeployNodes/gatherPreemptibleHolders/lookupVMClaimant/holderAddrFor/sortedHolderKeys
//   functions are all DELETED.
//
//   FLOOR-SLIM-proper Unit-8 moved the other 6 original host-seam impls (holderRunning/
//   holderStop[+wait]/holderStart/holderExists/gpuSwitchModeTolerant/ensureCDIRoot/
//   gpuCDIHolders/vmIsRunning/podIsRunning) DIRECTLY into candy/plugin-preempt
//   (holder_dispatch.go) — they were reached over this seam only because their ORIGINAL
//   implementation used charly-core-private mechanisms (providerRegistry,
//   connectPluginByWordRef), which now dispatch instead via the class-agnostic
//   specexec.Executor.InvokeProvider — never because the work itself needed a live LoadUnified
//   project. The readiness-gated stop-wait (formerly waitStoppedHost) uses spec.ReadinessProvider
//   directly in the plugin — the SAME project-aware resolver charly-core's own init() injects,
//   shared in-process by this compiled-in placement, so no new host seam was needed for it either.
//
// Crash-safety, the lease ledger, poison markers, liveness (owner PID/start), and the
// stop/flip/save sequencing all live in the plugin now; the host only supplies the ONE remaining
// resolved-project-backed config read (gatherResources, for its non-arbiter GPU-allocation
// caller) — no longer LoadUnified-coupled (K-wave W3a A2).

// envPreemptLeaseHeld is set by the OUTERMOST claim-bringing `charly` invocation (a check-bed
// run, or a standalone `charly vm create`/`charly start`) so the nested `charly` subprocesses it
// spawns do NOT independently acquire/release the lease — the owner manages it. Managed by the
// in-core shims (the arbiter plugin never sees the env).
const envPreemptLeaseHeld = "CHARLY_PREEMPT_LEASE"

// --- the in-core arbiter PROXY (dispatches to the compiled-in verb:arbiter plugin) ----------

// arbiterProxy is the in-core handle newResourceArbiter() returns to its current 3 callers
// (host_build_check_bed.go, host_build_arbiter_bracket.go, host_build_pod_lifecycle_dispatch.go —
// re-verified at Cutover B unit 6b). Its methods dispatch to the compiled-in
// candy/plugin-preempt (verb:arbiter) over an in-proc reverse channel — the arbiter runs there
// and calls back for its host seams.
type arbiterProxy struct{}

func newResourceArbiter() *arbiterProxy { return &arbiterProxy{} }

// arbiterInvoke resolves verb:arbiter and Invokes it with an action-tagged input, threading the
// IN-PROC reverse channel onto the ctx so the plugin's Invoke reaches its host seams over
// InvokeProvider/HostBuild (always-served generic seams — plugin_executor_reverse.go) — the SAME
// in-proc-executor pattern candy/plugin-box's dispatchBuild / candy/plugin-vm's resolveVmBuild use.
// Infra failures (no plugin, marshal, invoke)
// are returned as a Go error; a per-action OP failure rides reply.Error. This is the generic
// core→verb registry bridge the core lease-lifecycle callers use (core is not a plugin, so it
// cannot call InvokeProvider itself).
func arbiterInvoke(in spec.ArbiterInvokeInput) (spec.ArbiterInvokeReply, error) {
	prov, ok := providerRegistry.resolve(ClassVerb, "arbiter")
	if !ok {
		return spec.ArbiterInvokeReply{}, fmt.Errorf("resource arbiter (verb:arbiter) not registered — charly built without candy/plugin-preempt")
	}
	ctx := specexec.ContextWithExecutor(context.Background(),
		specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	reply, err := invokeTyped[spec.ArbiterInvokeInput, spec.ArbiterInvokeReply](ctx, prov, "arbiter", ops.OpRun, in)
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
// declares requires_shared — OR that implicitly consumes the nvidia GPU with no explicit claim
// (K-wave W3a A2): it ALWAYS dispatches to the arbiter (even with zero explicit shared tokens,
// unless an outer orchestrator already owns the lease) so the compiled-in candy/plugin-preempt
// can union its own implied-GPU-consumer token onto tokens (gpu_imply.go, now plugin-side) before
// applying its own early-return-on-empty acquire policy — arbiter policy belongs in the arbiter.
// Cheap: plugin-preempt is compiled-in, so this is an in-proc call, never a real RPC round trip.
func acquireSharedForClaimant(claimant string, node spec.BundleNode, transient bool) (*Lease, error) {
	if os.Getenv(envPreemptLeaseHeld) != "" {
		return &Lease{}, nil
	}
	return acquireDispatch(spec.ArbiterActionAcquireShared, claimant, dedupeNonEmpty(node.RequiredShared()), node, transient)
}

// acquireDispatch is the shared acquire leg (R3): it Invokes verb:arbiter with the pre-computed
// tokens + claim address, and on an active lease marks envPreemptLeaseHeld so nested
// subprocesses skip re-acquiring. It also projects the claimant node's GPU-relevant traits
// (IsGroup/IsPodMember/SecurityDevices) onto every action — cheap, ignored by every action except
// acquire-shared's implied-GPU-consumer check (K-wave W3a A2) — rather than branching per-action,
// since core already has the node in hand and the wire fields are optional/omitempty.
func acquireDispatch(action, claimant string, tokens []string, node spec.BundleNode, transient bool) (*Lease, error) {
	var secDevices []string
	if node.Security != nil {
		secDevices = node.Security.Devices
	}
	r, err := arbiterInvoke(spec.ArbiterInvokeInput{
		Action:          action,
		Claimant:        claimant,
		Tokens:          tokens,
		ClaimAddr:       spec.HolderAddrFor(claimant, node),
		Transient:       transient,
		IsGroup:         node.IsGroup(),
		IsPodMember:     spec.IsContainerVenue(&node),
		SecurityDevices: secDevices,
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
// declares requires_exclusive, otherwise SHARED (a no-op inside the arbiter when the claimant
// declares no requires_shared AND implies no GPU consumption). The single entry point for the
// start + check-bed paths (R3). A node that USES the nvidia GPU but declared NO explicit claim is
// auto-promoted to a SHARED claimant of the gpu token by the arbiter itself (K-wave W3a A2 — the
// former in-core withImpliedGPUShared moved to candy/plugin-preempt's gpu_imply.go) — so EVERY
// GPU-consuming deployment becomes a tracked, preemptable shared claimant with no per-deploy
// config.
func acquireResourceForClaimant(claimant string, node spec.BundleNode, transient bool) (*Lease, error) {
	if len(node.RequiredExclusive()) > 0 {
		return acquireExclusiveForClaimant(claimant, node, transient)
	}
	return acquireSharedForClaimant(claimant, node, transient)
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

// --- the arbiter HOST-SEAM impl that remains genuinely K1-blocked ---------------------------
//
// K1-unblock wave 1 retired gatherDeployNodes/gatherPreemptibleHolders/lookupVMClaimant/
// holderAddrFor from this file entirely: candy/plugin-preempt now reads its deploy tree off the
// generic InvokeProvider("build","project") envelope (rp.Deploy) instead of a bespoke "gather"
// reverse RPC, and does its own holder-filtering + VM-claimant lookup in-plugin via the portable
// sdk/deploykit helpers (FilterPreemptibleHolders/FindVMClaimant/HolderAddrFor/MergedDeployTree —
// the SAME functions host_build_config_resolve.go's VM-claimant lookup now shares, R3). The
// former bespoke arbiter reverse-RPC channel itself is deleted (sdk/protocol/schema/plugin.cue).
//
// gatherResources is the ONE function in this family that stays here: it has ONE remaining
// core-internal caller (gpu_allocate.go's bedGPUPrereqMissing — the pre-build check-bed
// fail-fast, reached now via the narrow host_build_check_bed_gpu_prereq.go seam that survived
// check-bed's full dissolution, #55 W3 B2-full — GPU host-DETECTION is the project's explicitly
// operator-dropped exception, fenced from this cutover; see that file's header). gpu_imply.go's
// former caller moved to candy/plugin-preempt (K-wave W3a A2) and no longer uses this function.
//
// gatherResources loads the token -> ResourceDef map (the gpu selector that drives the mode
// flip) via the SAME generic InvokeProvider("build","project") envelope every other
// resolved-project consumer uses (K-wave W3a A2 rewire) — replacing the former LoadUnified(".")
// K1-coupled read, retiring that dependency NOW rather than waiting for #12 (this function's own
// former "genuinely K1-blocked" status no longer holds). candy/plugin-build is COMPILED IN
// (charly/charly.yml compiled_plugins:), so this is the plain resolve+typed-reply shape —
// hostInvokeOr (provider_invoke.go), the SAME core→verb registry bridge arbiterInvoke uses for
// verb:arbiter — never a connect-on-demand (that machinery is for an EXTERNAL plugin; an R1
// self-correction of this function's first cut, which wrongly treated plugin-build as external).
// Zero value (nil Resources) on any resolve/invoke/decode failure — this function's existing "nil
// when none/unreadable" contract, unchanged.
func gatherResources() map[string]*spec.ResolvedResource {
	rp := hostInvokeOr[spec.ResolvedProjectRequest, spec.ResolvedProject](ClassBuild, "project", ops.OpResolve, spec.ResolvedProjectRequest{}, "gather-resources")
	return rp.Resources
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
