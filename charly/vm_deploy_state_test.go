package main

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"

	"github.com/opencharly/sdk/deploykit"
)

// saveVmDeployState must serialize the load→modify→save of the shared per-host
// overlay through acquireDeployConfigLock. Without the lock, concurrent writers
// (parallel `charly vm create` persist-auto-port, or a vm-create racing a
// `charly bundle add vm:<name>`) load→modify→save the same file and silently
// drop each other's entry. flock is per-open-fd, so concurrent goroutines in ONE
// process contend exactly like separate processes — this exercises the lock. The
// assertion is correctness: every concurrently-written entry survives.
func TestSaveVmDeployState_ConcurrentWritersAllSurvive(t *testing.T) {
	overlay := filepath.Join(t.TempDir(), "charly.yml")
	t.Setenv(DeployConfigEnv, overlay)

	const n = 12
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("vm:e%02d", i)
			errs[i] = saveVmDeployState(name, "", &spec.VmDeployState{SshPort: 3000 + i, Backend: "auto"})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d failed: %v", i, err)
		}
	}

	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	if dc == nil || dc.Bundle == nil {
		t.Fatal("no config persisted")
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("vm:e%02d", i)
		entry, ok := dc.Bundle[name]
		if !ok {
			t.Errorf("entry %q was lost — concurrent write race (lock not serializing)", name)
			continue
		}
		if entry.VmState == nil || entry.VmState.SshPort != 3000+i {
			t.Errorf("entry %q has wrong vm_state: %+v", name, entry.VmState)
		}
	}
}

// A single write round-trips, and the lock is released afterward (a second
// blocking write completes rather than self-deadlocking) — guards the
// acquire/defer-release balance.
func TestSaveVmDeployState_LockReleasedBetweenCalls(t *testing.T) {
	overlay := filepath.Join(t.TempDir(), "charly.yml")
	t.Setenv(DeployConfigEnv, overlay)

	if err := saveVmDeployState("vm:one", "", &spec.VmDeployState{SshPort: 2201}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// If the first call leaked the lock, this blocking acquire inside the second
	// call would hang the test (a self-deadlock surfaces as a timeout, never a
	// silent pass).
	if err := saveVmDeployState("vm:two", "", &spec.VmDeployState{SshPort: 2202}); err != nil {
		t.Fatalf("second write (lock not released?): %v", err)
	}
	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := dc.Bundle["vm:one"]; !ok {
		t.Error("vm:one lost across the second write")
	}
	if _, ok := dc.Bundle["vm:two"]; !ok {
		t.Error("vm:two not persisted")
	}
}

// TestRemoveVmDeployEntry_RemovesBundleKeyedBedEntry exercises the `vm:`-form From-scan in
// vmDeployEntryKeys: a kind:check VM bed (e.g. check-k3s-vm) writes its vm_state under the BUNDLE
// key (check-k3s-vm) cross-referencing the VM ENTITY (k3s-vm). The scan lets the DIRECT
// `charly vm destroy k3s-vm` path (which builds "vm:k3s-vm") still resolve the bundle-keyed entry
// via that cross-ref — an exact-key delete on "vm:k3s-vm" alone would miss it and leak it. (The
// per-deploy teardown does NOT rely on this scan — OpPostTeardown ships the deploy-name +
// vm:<domain> keys directly; see vmPostTeardown.) The From-scan must not over-match an UNRELATED
// bundle (check-other-vm, From=other-vm).
func TestRemoveVmDeployEntry_RemovesBundleKeyedBedEntry(t *testing.T) {
	overlay := filepath.Join(t.TempDir(), "charly.yml")
	t.Setenv(DeployConfigEnv, overlay)

	// Seed through the REAL write path under the bundle/bed key (dctx.Name) with
	// the resolved VM entity — exactly how the vm lifecycle hook PrepareVenue persists it.
	if err := saveVmDeployState("check-k3s-vm", "k3s-vm", &spec.VmDeployState{SshPort: 40161, Backend: "auto"}); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	// An UNRELATED VM bundle that must survive the k3s-vm teardown (no over-match).
	if err := saveVmDeployState("check-other-vm", "other-vm", &spec.VmDeployState{SshPort: 40162, Backend: "auto"}); err != nil {
		t.Fatalf("seed unrelated: %v", err)
	}

	// The write must carry the `vm:` cross-ref — the linkage teardown needs.
	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after seed: %v", err)
	}
	seeded, ok := dc.Bundle["check-k3s-vm"]
	if !ok {
		t.Fatal("seed did not write the check-k3s-vm bundle entry")
	}
	if seeded.From != "k3s-vm" {
		t.Fatalf("seed entry missing vm: cross-ref (teardown linkage): got %q", seeded.From)
	}

	// The DIRECT `charly vm destroy k3s-vm` path reaches removeVmDeployEntry with the prefixed
	// ENTITY form — NOT the bundle key the entry was written under. The From-scan bridges the gap.
	if err := removeVmDeployEntry("vm:k3s-vm"); err != nil {
		t.Fatalf("removeVmDeployEntry: %v", err)
	}

	got, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after teardown: %v", err)
	}
	if got == nil || got.Bundle == nil {
		t.Fatal("config vanished entirely")
	}
	if _, leaked := got.Bundle["check-k3s-vm"]; leaked {
		t.Error("bundle-keyed bed entry check-k3s-vm leaked after teardown (key-mismatch bug)")
	}
	if _, survived := got.Bundle["check-other-vm"]; !survived {
		t.Error("unrelated bundle check-other-vm was wrongly removed (over-match)")
	}
}

// TestPruneStaleVmDottedTwin moved to sdk/deploykit/vm_deploy_addressing_test.go (FLOOR-SLIM Unit
// 3) — PruneStaleVmDottedTwin is now a pure deploykit function, not a charly-core one.

// TestSaveVmDeployState_SelfHealsStaleDottedTwin is the end-to-end regression test: a real overlay
// file carrying a pre-fix poisoned dotted twin gets healed the next time the canonical domain is
// written, via a real saveVmDeployState call (no mocking — the same filesystem-backed pattern
// TestSaveVmDeployState_ConcurrentWritersAllSurvive uses).
func TestSaveVmDeployState_SelfHealsStaleDottedTwin(t *testing.T) {
	overlay := filepath.Join(t.TempDir(), "charly.yml")
	t.Setenv(DeployConfigEnv, overlay)

	// Seed a pre-fix poisoned overlay: a dotted twin alongside (what will become) the canonical entry.
	seed := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		"check-sidecar-pod.check-sidecar-pod-ephvm": {Target: "vm", VmState: &spec.VmDeployState{SshPort: 45551}},
	}}
	if err := saveBundleConfigNodeForm(seed); err != nil {
		t.Fatalf("seeding pre-fix overlay: %v", err)
	}

	// The canonical write — matches candy/plugin-vm's hostConfigPersist("vm:"+domainID, ...) call shape.
	if err := saveVmDeployState("vm:check-sidecar-pod-check-sidecar-pod-ephvm", "eval-vm", &spec.VmDeployState{SshPort: 33799}); err != nil {
		t.Fatalf("canonical write: %v", err)
	}

	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, stillPoisoned := dc.Bundle["check-sidecar-pod.check-sidecar-pod-ephvm"]; stillPoisoned {
		t.Error("the stale dotted twin survived the canonical write — self-heal did not fire")
	}
	entry, ok := dc.Bundle["vm:check-sidecar-pod-check-sidecar-pod-ephvm"]
	if !ok || entry.VmState == nil || entry.VmState.SshPort != 33799 {
		t.Errorf("canonical entry missing or wrong after self-heal: %+v", entry)
	}
}

// TestSaveVmDeployState_PreservesEphemeralOnSubsequentWrite is the regression test for the
// FINAL/K5 unit 6a RCA #7 live-probe-caught bug: registerEphemeralIfMarked
// (vm_lifecycle_preresolve.go) persists .VmState.Ephemeral under the canonical
// "vm:"+domainID key BEFORE `charly vm create`'s own state writes (e.g. the port_auto persist)
// run — RCA #6's key unification made this the COMMON ordering (the two writers never
// collided on separate keys before). A naive wholesale `entry.VmState = state` would silently
// ERASE the just-registered Ephemeral block, since the vm-create caller's state is never told
// about ephemeral registration. Proven against a real overlay file — no mocking.
func TestSaveVmDeployState_PreservesEphemeralOnSubsequentWrite(t *testing.T) {
	overlay := filepath.Join(t.TempDir(), "charly.yml")
	t.Setenv(DeployConfigEnv, overlay)

	const key = "vm:check-sidecar-pod-check-sidecar-pod-ephvm"

	// Step 1: the ephemeral registration write (mirrors persistEphemeralRuntime — seeds
	// Target/From + an Ephemeral block, the FIRST write to a fresh overlay).
	seed := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		key: {
			Target: "vm",
			From:   "eval-vm",
			VmState: &spec.VmDeployState{
				Ephemeral: &spec.EphemeralRuntime{ID: "abc123", Status: "active", DeployAddress: "check-sidecar-pod.check-sidecar-pod-ephvm"},
			},
		},
	}}
	if err := saveBundleConfigNodeForm(seed); err != nil {
		t.Fatalf("seeding ephemeral-registered overlay: %v", err)
	}

	// Step 2: `charly vm create`'s own state write — the SAME key, a state that knows NOTHING
	// about the ephemeral block (this is the exact shape vm_create_orchestrate.go constructs).
	if err := saveVmDeployState(key, "eval-vm", &spec.VmDeployState{SshPort: 41897, Backend: "auto"}); err != nil {
		t.Fatalf("vm-create state write: %v", err)
	}

	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	entry, ok := dc.Bundle[key]
	if !ok {
		t.Fatal("canonical entry vanished")
	}
	if entry.VmState == nil {
		t.Fatal("VmState vanished entirely")
	}
	if entry.VmState.SshPort != 41897 {
		t.Errorf("SshPort = %d, want 41897 (the vm-create write's own field)", entry.VmState.SshPort)
	}
	if entry.VmState.Ephemeral == nil {
		t.Fatal("Ephemeral block was ERASED by the subsequent vm-create write — the RCA #7 bug")
	}
	if entry.VmState.Ephemeral.ID != "abc123" {
		t.Errorf("Ephemeral.ID = %q, want \"abc123\" (preserved from the register-time write)", entry.VmState.Ephemeral.ID)
	}
}

// TestSaveVmDeployState_ReverseOrderingRoundTrips double-checks the REVERSE ordering (vm-create's
// state write lands FIRST, ephemeral registration SECOND) still round-trips correctly —
// persistEphemeralRuntime's own logic (candy/plugin-bundle/ephemeral.go) reads the EXISTING
// node.VmState and only sets .Ephemeral on it, never wholesale-replacing, so this direction was
// never at risk — this test documents and locks that in from the saveVmDeployState side (the
// state-carrying half of the round-trip, which IS this file's concern).
func TestSaveVmDeployState_ReverseOrderingRoundTrips(t *testing.T) {
	overlay := filepath.Join(t.TempDir(), "charly.yml")
	t.Setenv(DeployConfigEnv, overlay)

	const key = "vm:reverse-order-vm"

	// vm-create writes FIRST — no ephemeral knowledge yet.
	if err := saveVmDeployState(key, "eval-vm", &spec.VmDeployState{SshPort: 50001}); err != nil {
		t.Fatalf("vm-create state write: %v", err)
	}
	// A SECOND saveVmDeployState call carrying an Ephemeral block (mirrors what
	// persistEphemeralRuntime effectively produces when it runs after vm-create: it reads the
	// EXISTING entry, so the passed-in state already contains the merged prior fields).
	if err := saveVmDeployState(key, "eval-vm", &spec.VmDeployState{SshPort: 50001, Ephemeral: &spec.EphemeralRuntime{ID: "xyz789", Status: "active"}}); err != nil {
		t.Fatalf("ephemeral-carrying write: %v", err)
	}

	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	entry, ok := dc.Bundle[key]
	if !ok || entry.VmState == nil {
		t.Fatal("canonical entry missing")
	}
	if entry.VmState.SshPort != 50001 {
		t.Errorf("SshPort = %d, want 50001", entry.VmState.SshPort)
	}
	if entry.VmState.Ephemeral == nil || entry.VmState.Ephemeral.ID != "xyz789" {
		t.Errorf("Ephemeral = %+v, want ID=xyz789", entry.VmState.Ephemeral)
	}
}

// TestSplitVmAddress moved to sdk/vmshared/vm_deploy_addressing_test.go (FLOOR-SLIM Unit 3) —
// SplitVmAddress is now a pure vmshared function, not a charly-core one.

// TestSplitVmAddress_LedgerIdentityRegression is the regression test for the FINAL/K5 unit 6a
// RCA #9 live-probe-caught bug: `bundle del vm:<dotted-name>` silently no-op'd because
// deploykit.ComputeDeployID hashes the deploy name VERBATIM, and the raw "vm:"-prefixed CLI
// form hashed to a COMPLETELY DIFFERENT ID than the plain form the add-time tree walk used to
// record the ledger entry (verified live: 6413f8070aaa6087 vs d81fff596411fea4 for the exact
// same logical deployment). Proves vmshared.SplitVmAddress's stripped form produces the
// IDENTICAL deployID as the plain add-time form — the fix hostBuildDeployNodeDelDispatch relies
// on. Kept charly-side (rather than moved with the pure function) since it exercises the
// deploykit.ComputeDeployID interaction specifically, not the string-splitting logic alone.
func TestSplitVmAddress_LedgerIdentityRegression(t *testing.T) {
	const addTimeName = "check-sidecar-pod.check-sidecar-pod-ephvm"
	const delTimeAddress = "vm:check-sidecar-pod.check-sidecar-pod-ephvm"

	addID := deploykit.ComputeDeployID(addTimeName, nil, nil)

	// The BUG, preserved as a documented negative case: computing the ID from the raw,
	// unstripped CLI address produces a DIFFERENT id than the ledger was recorded under.
	buggyDelID := deploykit.ComputeDeployID(delTimeAddress, nil, nil)
	if buggyDelID == addID {
		t.Fatalf("test assumption broken: the raw prefixed form no longer collides differently (got %q == %q) — re-verify ComputeDeployID's contract before trusting this regression test", buggyDelID, addID)
	}

	// THE FIX: strip via vmshared.SplitVmAddress before computing the ID — must match the
	// add-time ID.
	plain, isVm := vmshared.SplitVmAddress(delTimeAddress)
	if !isVm {
		t.Fatal("vmshared.SplitVmAddress did not recognize the vm: prefix")
	}
	fixedDelID := deploykit.ComputeDeployID(plain, nil, nil)
	if fixedDelID != addID {
		t.Errorf("ComputeDeployID(vmshared.SplitVmAddress(%q)) = %q, want %q (the add-time ID) — the ledger record would still be unreachable from the del path", delTimeAddress, fixedDelID, addID)
	}
}

// TestVmLifecyclePostTeardown_UsesCanonicalKey is the regression test for the FINAL/K5 unit 6a
// RCA #9 live-probe-caught bug: vmLifecyclePostTeardown used to look up the per-host overlay by
// the RAW deploy name via a bare LookupKey exact match — but every vm writer persists under the
// canonical "vm:"+VmDomainIdentity(name) key (dashes, not dots), so the raw name NEVER matched
// and TeardownEphemeralLifecycle never fired. Proven end-to-end against a real overlay file: a
// canonically-keyed ephemeral entry gets its Ephemeral block cleared by a single
// vmLifecyclePostTeardown call, with no mocking of the dispatch chain.
func TestVmLifecyclePostTeardown_UsesCanonicalKey(t *testing.T) {
	overlay := filepath.Join(t.TempDir(), "charly.yml")
	t.Setenv(DeployConfigEnv, overlay)

	const dottedName = "check-sidecar-pod.check-sidecar-pod-ephvm"
	const canonicalKey = "vm:check-sidecar-pod-check-sidecar-pod-ephvm"

	seed := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		canonicalKey: {
			Target: "vm",
			From:   "eval-vm",
			VmState: &spec.VmDeployState{
				SshPort: 12345,
				Ephemeral: &spec.EphemeralRuntime{
					ID:            "test-id",
					Status:        "active",
					DeployAddress: dottedName,
				},
			},
		},
	}}
	if err := saveBundleConfigNodeForm(seed); err != nil {
		t.Fatalf("seeding overlay: %v", err)
	}

	if err := vmLifecyclePostTeardown(dottedName, nil); err != nil {
		t.Fatalf("vmLifecyclePostTeardown: %v", err)
	}

	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	entry, ok := dc.Bundle[canonicalKey]
	if !ok {
		t.Fatal("canonical entry vanished entirely — teardown should only clear Ephemeral, not the whole entry")
	}
	if entry.VmState != nil && entry.VmState.Ephemeral != nil {
		t.Error("Ephemeral block was NOT cleared — the canonical-key lookup did not find the entry")
	}
}
