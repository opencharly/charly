package main

import (
	"fmt"
	"github.com/opencharly/sdk/spec"
	"path/filepath"
	"sync"
	"testing"

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
