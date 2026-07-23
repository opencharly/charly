package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
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
//   2. gatherResources — the ONE arbiter host dependency that remains genuinely K1-blocked
//      (LoadUnified-coupled), and ONLY because it has core-internal callers OUTSIDE the arbiter
//      (gpu_allocate.go/gpu_imply.go — the operator-deferred GPU auto-allocation exception, not
//      K-wave inventory). The arbiter itself no longer reaches it: candy/plugin-preempt reads
//      resources off the generic HostBuild("resolved-project") envelope (rp.Resources) instead.
//
//   K1-unblock wave 1 retired the ExecutorService.HostArbiter reverse RPC entirely (the former
//   "2 genuinely K1-blocked HostArbiter seams" — gather/resources): candy/plugin-preempt now
//   reads its deploy tree + resources off HostBuild("resolved-project") and does its own
//   holder-filtering/VM-claimant-lookup in-plugin via the portable sdk/deploykit helpers
//   (FilterPreemptibleHolders/FindVMClaimant/HolderAddrFor/MergedDeployTree), the SAME functions
//   host_build_config_resolve.go's VM-claimant lookup now shares (R3). charly/arbiter_host.go,
//   the HostArbiter RPC method + its request/reply proto messages, and the local
//   gatherDeployNodes/gatherPreemptibleHolders/lookupVMClaimant/holderAddrFor/sortedHolderKeys
//   functions are all DELETED.
//
//   FLOOR-SLIM-proper Unit-8 moved the other 6 original host-seam impls (holderRunning/
//   holderStop[+wait]/holderStart/holderExists/gpuSwitchModeTolerant/ensureCDIRoot/
//   gpuCDIHolders/vmIsRunning/podIsRunning) DIRECTLY into candy/plugin-preempt
//   (holder_dispatch.go) — they were reached over this seam only because their ORIGINAL
//   implementation used charly-core-private mechanisms (providerRegistry,
//   connectPluginByWordRef), which now dispatch instead via the class-agnostic
//   sdk.Executor.InvokeProvider — never because the work itself needed a live LoadUnified
//   project. The readiness-gated stop-wait (formerly waitStoppedHost) uses kit.ReadinessProvider
//   directly in the plugin — the SAME project-aware resolver charly-core's own init() injects,
//   shared in-process by this compiled-in placement, so no new host seam was needed for it either.
//
// Crash-safety, the lease ledger, poison markers, liveness (owner PID/start), and the
// stop/flip/save sequencing all live in the plugin now; the host only supplies the ONE remaining
// LoadUnified-coupled config read (gatherResources, for its non-arbiter GPU-allocation callers).

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
// dispatchBuild in-proc-executor pattern (build.go). Infra failures (no plugin, marshal, invoke)
// are returned as a Go error; a per-action OP failure rides reply.Error. This is the generic
// core→verb registry bridge the core lease-lifecycle callers use (core is not a plugin, so it
// cannot call InvokeProvider itself).
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
		ClaimAddr: deploykit.HolderAddrFor(claimant, node),
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

// --- the arbiter HOST-SEAM impl that remains genuinely K1-blocked ---------------------------
//
// K1-unblock wave 1 retired gatherDeployNodes/gatherPreemptibleHolders/lookupVMClaimant/
// holderAddrFor from this file entirely: candy/plugin-preempt now reads its deploy tree off the
// generic HostBuild("resolved-project") envelope (rp.Deploy) instead of a bespoke HostArbiter
// "gather" RPC, and does its own holder-filtering + VM-claimant lookup in-plugin via the portable
// sdk/deploykit helpers (FilterPreemptibleHolders/FindVMClaimant/HolderAddrFor/MergedDeployTree —
// the SAME functions host_build_config_resolve.go's VM-claimant lookup now shares, R3). The
// ExecutorService.HostArbiter reverse RPC itself is deleted (sdk/protocol/schema/plugin.cue).
//
// gatherResources is the ONE function in this family that stays here: it has core-internal
// callers OUTSIDE the arbiter (gpu_allocate.go, gpu_imply.go — the operator-deferred GPU
// auto-allocation exception, not K-wave inventory; see charly/devices.go), so it cannot move.

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
