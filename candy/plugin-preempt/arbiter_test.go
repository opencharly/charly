package preempt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/charly/charly/spec"
)

// arbiter_test.go — the SEAM-FAKED arbiter unit suite, relocated with the arbiter from charly
// core (the former charly/preempt_test.go + preempt_shared_test.go) and adapted to the C9 seam
// interface: the seams are now the HOST-projected shapes (gather -> []spec.HolderDescriptor,
// resources -> map[token]vendor, switchMode -> (wedged, err)) the arbiter reaches over the
// reverse channel, and the acquire methods take pre-computed tokens + claim addr (the in-core
// shim's job). The fakes model the host so the coordination logic is tested without a live host.

// fakeWorld is an in-memory model of deployment running-state + the host operations the arbiter
// performs. Keyed by deploy name (HolderAddr.Name).
type fakeWorld struct {
	running   map[string]bool
	ops       []string
	stopErr   map[string]bool   // names whose stop() should fail
	resources map[string]string // token -> gpu vendor (gpu-backed only; arbitration-only omitted)
	modes     map[string]string // vendor -> last mode flipped to
	cdiCalls  int               // ensureCDI invocations
	wedge     bool              // when true, switchMode returns wedged=true + an error
}

// newTestArbiter builds an arbiter whose seams are backed by the fake world (a temp ledger dir).
func newTestArbiter(t *testing.T, holders []spec.HolderDescriptor, w *fakeWorld) *ResourceArbiter {
	t.Helper()
	return &ResourceArbiter{
		ledgerPath: filepath.Join(t.TempDir(), "leases.yml"),
		gather:     func() []spec.HolderDescriptor { return holders },
		running:    func(addr spec.HolderAddr) bool { return w.running[addr.Name] },
		stop: func(addr spec.HolderAddr) error {
			if w.stopErr[addr.Name] {
				w.ops = append(w.ops, "stop-FAIL:"+addr.Name)
				return errStopFailed
			}
			w.running[addr.Name] = false
			w.ops = append(w.ops, "stop:"+addr.Name)
			return nil
		},
		start: func(addr spec.HolderAddr) error {
			w.running[addr.Name] = true
			w.ops = append(w.ops, "start:"+addr.Name)
			return nil
		},
		resources: func() map[string]string { return w.resources },
		switchMode: func(vendor, mode string) (bool, error) {
			if w.modes == nil {
				w.modes = map[string]string{}
			}
			if w.wedge {
				w.ops = append(w.ops, "mode-WEDGE:"+mode+":"+vendor)
				return true, spec.ErrGPUSwitchWedged
			}
			w.modes[vendor] = mode
			w.ops = append(w.ops, "mode:"+mode+":"+vendor)
			return false, nil
		},
		ensureCDI: func() { w.cdiCalls++ },
		nowUTC:    func() string { return "2026-01-01T00:00:00Z" },
	}
}

var errStopFailed = &stopFailedError{}

type stopFailedError struct{}

func (*stopFailedError) Error() string { return "stop failed (test)" }

// holderDesc is a host-projected preemptible holder (Name + Holds + Addr + Restore) — the shape
// the gather seam returns.
func holderDesc(name string, holds []string, restore string) spec.HolderDescriptor {
	return spec.HolderDescriptor{
		Name:    name,
		Holds:   holds,
		Addr:    spec.HolderAddr{Name: name, Target: "pod", Base: name},
		Restore: restore,
	}
}

// claimAddr is the claimant deployment address the in-core shim pre-computes.
func claimAddr(name string) spec.HolderAddr {
	return spec.HolderAddr{Name: name, Target: "pod", Base: name}
}

func opsContain(ops []string, want string) bool {
	for _, o := range ops {
		if o == want {
			return true
		}
	}
	return false
}

func gpuResources() map[string]string { return map[string]string{"nvidia-gpu": "0x10de"} }

// 1. Acquire stops a running holder of the token; Release restores it.
func TestArbiter_AcquireStopsHolder_ReleaseRestores(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "claimant": true}}
	holders := []spec.HolderDescriptor{holderDesc("h1", []string{"gpu"}, "")}
	a := newTestArbiter(t, holders, w)

	active, err := a.AcquireExclusive("claimant", []string{"gpu"}, claimAddr("claimant"), true)
	if err != nil || !active {
		t.Fatalf("acquire: active=%v err=%v", active, err)
	}
	if w.running["h1"] {
		t.Fatalf("holder h1 should be stopped after acquire; ops=%v", w.ops)
	}
	led, _, _ := a.Status()
	if len(led.Leases) != 1 || led.Leases[0].Claimant != "claimant" {
		t.Fatalf("expected one lease for claimant, got %+v", led.Leases)
	}
	if err := a.ReleaseClaimant("claimant", true); err != nil {
		t.Fatalf("release: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("holder h1 should be restored after release; ops=%v", w.ops)
	}
	led, _, _ = a.Status()
	if len(led.Leases) != 0 {
		t.Fatalf("lease should be gone after release, got %+v", led.Leases)
	}
}

// 2. A holder already stopped before the claim is neither stopped nor restored.
func TestArbiter_AlreadyStoppedHolder_LeftAlone(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": false, "claimant": true}}
	holders := []spec.HolderDescriptor{holderDesc("h1", []string{"gpu"}, "")}
	a := newTestArbiter(t, holders, w)

	if _, err := a.AcquireExclusive("claimant", []string{"gpu"}, claimAddr("claimant"), true); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if opsContain(w.ops, "stop:h1") {
		t.Fatalf("already-stopped holder must not be stopped; ops=%v", w.ops)
	}
	_ = a.ReleaseClaimant("claimant", true)
	if opsContain(w.ops, "start:h1") {
		t.Fatalf("must not start a holder we never stopped; ops=%v", w.ops)
	}
}

// 3. A second claimant of an overlapping token is refused while the first lease is live.
func TestArbiter_ConflictRefusesSecondClaimant(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "c1": true, "c2": true}}
	holders := []spec.HolderDescriptor{holderDesc("h1", []string{"gpu"}, "")}
	a := newTestArbiter(t, holders, w)

	if _, err := a.AcquireExclusive("c1", []string{"gpu"}, claimAddr("c1"), false); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	_, err := a.AcquireExclusive("c2", []string{"gpu"}, claimAddr("c2"), false)
	if err == nil || !strings.Contains(err.Error(), "already claimed by") {
		t.Fatalf("expected conflict error for c2, got %v", err)
	}
}

// 4. A holder whose token does not overlap the claim is untouched.
func TestArbiter_NonOverlappingTokensIgnored(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "claimant": true}}
	holders := []spec.HolderDescriptor{holderDesc("h1", []string{"other"}, "")}
	a := newTestArbiter(t, holders, w)

	if _, err := a.AcquireExclusive("claimant", []string{"gpu"}, claimAddr("claimant"), true); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("non-overlapping holder h1 must not be stopped; ops=%v", w.ops)
	}
}

// 5. reconcileStranded restores holders for a lease whose claimant is gone.
func TestArbiter_ReconcileStrandedRestores(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": false, "deadclaimant": false}}
	a := newTestArbiter(t, nil, w)

	led := &spec.PreemptLedger{Leases: []spec.PreemptLease{{
		Claimant: "deadclaimant",
		Claim:    spec.HolderAddr{Name: "deadclaimant", Target: "pod", Base: "deadclaimant"},
		Tokens:   []string{"gpu"},
		Preempted: []spec.PreemptedHolder{{
			Addr:    spec.HolderAddr{Name: "h1", Target: "pod", Base: "h1"},
			Holds:   []string{"gpu"},
			Restore: spec.PreemptRestoreAlways,
		}},
	}}}
	if err := a.saveLedger(led); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := a.reconcileStranded(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("stranded holder h1 should be restored; ops=%v", w.ops)
	}
	got, _, _ := a.Status()
	if len(got.Leases) != 0 {
		t.Fatalf("stranded lease should be pruned, got %+v", got.Leases)
	}
}

// 5b. A TRANSIENT lease whose OWNER process is alive is NOT reconciled (build phase).
func TestArbiter_TransientLeaseLiveOwnerNotReconciled(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"building-bed": false}}
	a := newTestArbiter(t, nil, w)

	led := &spec.PreemptLedger{Leases: []spec.PreemptLease{{
		Claimant:   "building-bed",
		Claim:      spec.HolderAddr{Name: "building-bed", Target: "pod", Base: "building-bed"},
		Tokens:     []string{"gpu"},
		Shared:     true,
		Transient:  true,
		OwnerPID:   os.Getpid(),
		OwnerStart: selfProcStart(),
		Created:    "2026-01-01T00:00:00Z",
	}}}
	if err := a.saveLedger(led); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := a.reconcileStranded(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, stranded, _ := a.Status()
	if len(got.Leases) != 1 {
		t.Fatalf("a live-owner transient lease must NOT be reconciled, got %+v", got.Leases)
	}
	if len(stranded) != 0 {
		t.Fatalf("a live-owner transient lease must not display STRANDED, got %v", stranded)
	}
}

// 5c. A TRANSIENT lease whose OWNER is gone (PID reused) IS reconciled.
func TestArbiter_TransientLeaseDeadOwnerReconciled(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": false, "crashed-bed": true}}
	a := newTestArbiter(t, nil, w)

	led := &spec.PreemptLedger{Leases: []spec.PreemptLease{{
		Claimant:   "crashed-bed",
		Claim:      spec.HolderAddr{Name: "crashed-bed", Target: "pod", Base: "crashed-bed"},
		Tokens:     []string{"gpu"},
		Shared:     true,
		Transient:  true,
		OwnerPID:   os.Getpid(),
		OwnerStart: "0", // start-time mismatch ⇒ PID reused ⇒ owner gone
		Preempted: []spec.PreemptedHolder{{
			Addr:    spec.HolderAddr{Name: "h1", Target: "pod", Base: "h1"},
			Holds:   []string{"gpu"},
			Restore: spec.PreemptRestoreAlways,
		}},
	}}}
	if err := a.saveLedger(led); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := a.reconcileStranded(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("a dead-owner lease's holder h1 should be restored; ops=%v", w.ops)
	}
	got, _, _ := a.Status()
	if len(got.Leases) != 0 {
		t.Fatalf("a dead-owner transient lease should be reconciled away, got %+v", got.Leases)
	}
}

// 5d. Two concurrent SHARED claimants coexist (refcount=2); the token flips to nvidia.
func TestArbiter_TwoConcurrentSharedLeasesCoexist(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{}, resources: map[string]string{"gpu": "0x10de"}}
	a := newTestArbiter(t, nil, w)

	if _, err := a.AcquireShared("bed-a", []string{"gpu"}, claimAddr("bed-a"), true); err != nil {
		t.Fatalf("acquire bed-a: %v", err)
	}
	if _, err := a.AcquireShared("bed-b", []string{"gpu"}, claimAddr("bed-b"), true); err != nil {
		t.Fatalf("acquire bed-b: %v", err)
	}
	led, stranded, _ := a.Status()
	if len(led.Leases) != 2 {
		t.Fatalf("two concurrent shared claimants must hold TWO leases, got %d: %+v", len(led.Leases), led.Leases)
	}
	if len(stranded) != 0 {
		t.Fatalf("neither concurrent shared lease should be STRANDED, got %v", stranded)
	}
	if w.modes["0x10de"] != spec.GpuModeNvidia {
		t.Fatalf("shared claims should leave the token in nvidia mode, got %q", w.modes["0x10de"])
	}
}

// 6a. restore: on-success — a FAILED claim leaves the holder stopped.
func TestArbiter_RestoreOnSuccess_FailedClaimLeavesStopped(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "claimant": true}}
	holders := []spec.HolderDescriptor{holderDesc("h1", []string{"gpu"}, spec.PreemptRestoreSuccess)}
	a := newTestArbiter(t, holders, w)

	if _, err := a.AcquireExclusive("claimant", []string{"gpu"}, claimAddr("claimant"), true); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if w.running["h1"] {
		t.Fatal("holder should be stopped after acquire")
	}
	if err := a.ReleaseClaimant("claimant", false); err != nil {
		t.Fatalf("release-failed: %v", err)
	}
	if w.running["h1"] {
		t.Fatalf("on-success holder must stay stopped on a failed claim; ops=%v", w.ops)
	}
}

// 6b. restore: always — even a FAILED claim restores the holder.
func TestArbiter_RestoreAlways_FailedClaimRestores(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "claimant": true}}
	holders := []spec.HolderDescriptor{holderDesc("h1", []string{"gpu"}, spec.PreemptRestoreAlways)}
	a := newTestArbiter(t, holders, w)

	if _, err := a.AcquireExclusive("claimant", []string{"gpu"}, claimAddr("claimant"), true); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := a.ReleaseClaimant("claimant", false); err != nil {
		t.Fatalf("release-failed: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("always-restore holder must come back even on failure; ops=%v", w.ops)
	}
}

// 7. The ledger persists across arbiter instances (durable, crash-recoverable).
func TestArbiter_LedgerPersistsAcrossInstances(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "claimant": true}}
	holders := []spec.HolderDescriptor{holderDesc("h1", []string{"gpu"}, "")}
	a1 := newTestArbiter(t, holders, w)
	if _, err := a1.AcquireExclusive("claimant", []string{"gpu"}, claimAddr("claimant"), false); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	a2 := &ResourceArbiter{
		ledgerPath: a1.ledgerPath,
		gather:     a1.gather,
		running:    a1.running,
		stop:       a1.stop,
		start:      a1.start,
		resources:  a1.resources,
		switchMode: a1.switchMode,
		ensureCDI:  a1.ensureCDI,
		nowUTC:     a1.nowUTC,
	}
	led, _, _ := a2.Status()
	if len(led.Leases) != 1 {
		t.Fatalf("a2 should see the persisted lease, got %+v", led.Leases)
	}
	if err := a2.ReleaseClaimant("claimant", true); err != nil {
		t.Fatalf("a2 release: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("a2 release should restore h1; ops=%v", w.ops)
	}
}

// 8. A claimant that requires nothing gets a no-op (inactive) lease.
func TestArbiter_NoTokensIsNoop(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true}}
	holders := []spec.HolderDescriptor{holderDesc("h1", []string{"gpu"}, "")}
	a := newTestArbiter(t, holders, w)
	active, err := a.AcquireExclusive("claimant", nil, claimAddr("claimant"), true)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if active {
		t.Fatal("a no-token claim must be an inactive (no-op) lease")
	}
	if !w.running["h1"] {
		t.Fatal("no-token claim must not stop any holder")
	}
}

// 9. A stop failure rolls back: holders already stopped this call are restarted, no phantom lease.
func TestArbiter_StopFailureRollsBack(t *testing.T) {
	w := &fakeWorld{
		running: map[string]bool{"h1": true, "h2": true, "claimant": true},
		stopErr: map[string]bool{"h2": true},
	}
	holders := []spec.HolderDescriptor{
		holderDesc("h1", []string{"gpu"}, ""),
		holderDesc("h2", []string{"gpu"}, ""),
	}
	a := newTestArbiter(t, holders, w)

	_, err := a.AcquireExclusive("claimant", []string{"gpu"}, claimAddr("claimant"), true)
	if err == nil {
		t.Fatal("expected acquire to fail when a holder stop fails")
	}
	if !w.running["h1"] {
		t.Fatalf("h1 should be rolled back to running after the failed acquire; ops=%v", w.ops)
	}
	led, _, _ := a.Status()
	if len(led.Leases) != 0 {
		t.Fatalf("no lease should remain after a rolled-back acquire, got %+v", led.Leases)
	}
}

// --- shared-claim tests (from preempt_shared_test.go) ----------------------------------------

// S1. The FIRST shared claim flips the gpu-backed token to nvidia mode + CDI.
func TestArbiter_SharedAcquireFlipsNvidia(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"pod1": true}, resources: gpuResources()}
	a := newTestArbiter(t, nil, w)

	if _, err := a.AcquireShared("pod1", []string{"nvidia-gpu"}, claimAddr("pod1"), false); err != nil {
		t.Fatalf("acquire-shared: %v", err)
	}
	if w.modes["0x10de"] != spec.GpuModeNvidia {
		t.Fatalf("first shared claim must flip to nvidia; modes=%v ops=%v", w.modes, w.ops)
	}
	if w.cdiCalls == 0 {
		t.Fatal("first shared claim must (re)generate CDI")
	}
	led, _, _ := a.Status()
	if len(led.Leases) != 1 || !led.Leases[0].Shared {
		t.Fatalf("expected one SHARED lease, got %+v", led.Leases)
	}
}

// S2. Two shared claims coexist (refcount); the last release flips back to vfio.
func TestArbiter_TwoSharedCoexist_LastReleaseRestoresVfio(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"pod1": true, "pod2": true}, resources: gpuResources()}
	a := newTestArbiter(t, nil, w)

	if _, err := a.AcquireShared("pod1", []string{"nvidia-gpu"}, claimAddr("pod1"), false); err != nil {
		t.Fatalf("pod1 acquire: %v", err)
	}
	if _, err := a.AcquireShared("pod2", []string{"nvidia-gpu"}, claimAddr("pod2"), false); err != nil {
		t.Fatalf("pod2 acquire: %v", err)
	}
	if err := a.ReleaseClaimant("pod1", true); err != nil {
		t.Fatalf("pod1 release: %v", err)
	}
	if w.modes["0x10de"] != spec.GpuModeNvidia {
		t.Fatalf("token still shared by pod2 → must stay nvidia; modes=%v", w.modes)
	}
	if err := a.ReleaseClaimant("pod2", true); err != nil {
		t.Fatalf("pod2 release: %v", err)
	}
	if w.modes["0x10de"] != spec.GpuModeVfio {
		t.Fatalf("last shared release must flip back to vfio; modes=%v ops=%v", w.modes, w.ops)
	}
}

// S3. An EXCLUSIVE claim PREEMPTS running shared pods and flips the token to vfio.
func TestArbiter_ExclusivePreemptsShared(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"pod1": true, "vm": true}, resources: gpuResources()}
	a := newTestArbiter(t, nil, w)

	if _, err := a.AcquireShared("pod1", []string{"nvidia-gpu"}, claimAddr("pod1"), false); err != nil {
		t.Fatalf("pod1 shared acquire: %v", err)
	}
	if _, err := a.AcquireExclusive("vm", []string{"nvidia-gpu"}, claimAddr("vm"), false); err != nil {
		t.Fatalf("vm exclusive acquire: %v", err)
	}
	if w.running["pod1"] {
		t.Fatalf("exclusive claim must stop the shared pod; ops=%v", w.ops)
	}
	if w.modes["0x10de"] != spec.GpuModeVfio {
		t.Fatalf("exclusive claim must flip token to vfio; modes=%v", w.modes)
	}
	led, _, _ := a.Status()
	if len(led.Leases) != 1 || led.Leases[0].Shared || led.Leases[0].Claimant != "vm" {
		t.Fatalf("expected one EXCLUSIVE lease for vm, got %+v", led.Leases)
	}
}

// S4. A shared claim is REFUSED while an exclusive claim holds the token.
func TestArbiter_SharedRefusedUnderExclusive(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"vm": true, "pod1": true}, resources: gpuResources()}
	a := newTestArbiter(t, nil, w)

	if _, err := a.AcquireExclusive("vm", []string{"nvidia-gpu"}, claimAddr("vm"), false); err != nil {
		t.Fatalf("vm exclusive acquire: %v", err)
	}
	_, err := a.AcquireShared("pod1", []string{"nvidia-gpu"}, claimAddr("pod1"), false)
	if err == nil || !strings.Contains(err.Error(), "EXCLUSIVELY") {
		t.Fatalf("shared claim under an exclusive hold must be refused, got %v", err)
	}
}

// S5. A shared claim PREEMPTS a running preemptible HOLDER; the last release RESTORES it.
func TestArbiter_SharedPreemptsHolder_ReleaseRestores(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "pod1": true}, resources: gpuResources()}
	holders := []spec.HolderDescriptor{holderDesc("h1", []string{"nvidia-gpu"}, "")}
	a := newTestArbiter(t, holders, w)

	if _, err := a.AcquireShared("pod1", []string{"nvidia-gpu"}, claimAddr("pod1"), false); err != nil {
		t.Fatalf("pod1 shared acquire: %v", err)
	}
	if w.running["h1"] {
		t.Fatalf("shared claim must preempt the vfio holder h1; ops=%v", w.ops)
	}
	if err := a.ReleaseClaimant("pod1", true); err != nil {
		t.Fatalf("release: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("last shared release must restore the preempted holder h1; ops=%v", w.ops)
	}
	if w.modes["0x10de"] != spec.GpuModeVfio {
		t.Fatalf("last shared release must flip token back to vfio; modes=%v", w.modes)
	}
}

// S6. A selector-less (abstract) shared token refcounts WITHOUT any device flip.
func TestArbiter_SharedAbstractTokenNoFlip(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"pod1": true}, resources: map[string]string{}} // abstract token not gpu-backed
	a := newTestArbiter(t, nil, w)

	if _, err := a.AcquireShared("pod1", []string{"abstract"}, claimAddr("pod1"), false); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if len(w.modes) != 0 {
		t.Fatalf("a selector-less token must not trigger a device flip; modes=%v", w.modes)
	}
	if w.cdiCalls != 0 {
		t.Fatalf("a selector-less token must not generate CDI; cdiCalls=%d", w.cdiCalls)
	}
}

// --- resource poisoning (device_lock wedge cascade containment) ------------------------------

func TestArbiter_PoisonRoundTripAndStale(t *testing.T) {
	if bootID() == "" {
		t.Skip("no /proc/sys/kernel/random/boot_id on this host")
	}
	a := newTestArbiter(t, nil, &fakeWorld{running: map[string]bool{}})

	a.poisonResource("nvidia-gpu")
	if !a.resourcePoisoned("nvidia-gpu") {
		t.Fatal("token must read poisoned right after poisonResource (same boot)")
	}
	if err := os.WriteFile(a.poisonPath("nvidia-gpu"), []byte("some-old-boot-id\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a.resourcePoisoned("nvidia-gpu") {
		t.Error("a prior-boot marker must read NOT poisoned (the reboot cleared the wedge)")
	}
	if _, err := os.Stat(a.poisonPath("nvidia-gpu")); !os.IsNotExist(err) {
		t.Error("a stale marker must be removed on read")
	}
	a.poisonResource("nvidia-gpu")
	a.clearPoison("nvidia-gpu")
	if a.resourcePoisoned("nvidia-gpu") {
		t.Error("clearPoison must remove the marker")
	}
}

func TestApplyMode_WedgePoisonsToken(t *testing.T) {
	if bootID() == "" {
		t.Skip("no boot_id")
	}
	w := &fakeWorld{resources: gpuResources(), wedge: true}
	a := newTestArbiter(t, nil, w)

	if err := a.applyMode([]string{"nvidia-gpu"}, spec.GpuModeVfio); err == nil {
		t.Fatal("applyMode must surface the wedge error")
	}
	if !a.resourcePoisoned("nvidia-gpu") {
		t.Error("a wedge during applyMode must POISON the token (cascade containment)")
	}
	// A subsequent applyMode refuses the poisoned token WITHOUT calling switchMode.
	switched := false
	a.switchMode = func(string, string) (bool, error) { switched = true; return false, nil }
	if err := a.applyMode([]string{"nvidia-gpu"}, spec.GpuModeVfio); err == nil {
		t.Errorf("poisoned token must keep being refused, got nil")
	}
	if switched {
		t.Error("a poisoned token must NOT reach switchMode (would re-wedge)")
	}
}

func TestArbiter_PoisonedTokenRefusesAcquire(t *testing.T) {
	if bootID() == "" {
		t.Skip("no boot_id")
	}
	w := &fakeWorld{running: map[string]bool{}, resources: gpuResources()}
	a := newTestArbiter(t, nil, w)
	a.poisonResource("nvidia-gpu")

	if _, err := a.AcquireShared("gpu-bed", []string{"nvidia-gpu"}, claimAddr("gpu-bed"), true); err == nil || !strings.Contains(err.Error(), "reboot") {
		t.Fatalf("AcquireShared on a poisoned token must refuse with a reboot-required error, got %v", err)
	}
	if _, err := a.AcquireExclusive("gpu-vm", []string{"nvidia-gpu"}, claimAddr("gpu-vm"), true); err == nil || !strings.Contains(err.Error(), "reboot") {
		t.Fatalf("AcquireExclusive on a poisoned token must refuse with a reboot-required error, got %v", err)
	}
}
