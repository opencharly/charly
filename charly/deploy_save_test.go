package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
)

// The SaveDeployState-based tests below (TestSaveDeployState_AbortOnInvalidExistingFile,
// _PersistsImageAndTargetForNewEntry, _DoesNotClobberExistingImageTarget) and
// TestRemoveVmDeployEntry_SelectiveAndIdempotent were converted (team-lead directive,
// 2026-08-03 seam-drive spike) to drive the REAL production seam
// (dispatchDeployTarget → command:bundle's Invoke(OpDeployDispatch) → handleDeployApply/Del →
// persistDeployState/deploykit.RemoveVmDeployEntry) via deploy_dispatch_seam_test_helpers_test.go's
// testDispatchLifecycleAdd/Del, instead of calling deploykit.SaveDeployState/RemoveVmDeployEntry
// directly — HIGHER fidelity (round-trips production's actual write path, not just the library
// call) and it kills the sdk import for these four.
//
// TestSaveBundleConfig_AtomicWriteLeavesNoTempLeftover / _RefusesToClobberUnloadableConfig /
// TestBundleNode_DisposableFalseRoundTrip relocated (#55 final-tail split-by-assertion round,
// team-lead directive 2026-08-03): each split into a WRITER-BEHAVIOR half (deploykit.
// SaveBundleConfig's OWN atomic-write / abort-on-unloadable-read / explicit-false round-trip
// contract — genuine deploykit-mechanism behavior) that moved to
// candy/plugin-bundle/deploy_state_writer_test.go, and (DisposableFalseRoundTrip only) a
// CHARLY-LOADER half (does LoadUnified correctly parse the *bool Disposable field) that stays
// here as TestBundleNode_DisposableFalseRoundTrip_Loader, reading a checked-in literal fixture —
// no sdk import needed for either half.

// TestDeployConfigLookup_NilSafe / TestDeployConfigLookup_PresentAndAbsent relocated to
// candy/plugin-bundle (#55 decoupling, Batch A) — pure in-memory *deploykit.BundleConfig
// fixtures, zero charly dep.

// TestSaveDeployState_AbortOnInvalidExistingFile pins the post-2026-05-16
// data-loss fix: when LoadBundleConfig returns an error (e.g. because
// the file fails spec.ValidateDeployRequiresBox), saveDeployState MUST
// ABORT and leave the file byte-identical — not silently construct a
// fresh empty config and truncate the on-disk file.
//
// Pre-fix reproduction: `charly bundle add arch arch --disposable`
// against a deploy.yml whose pre-existing entries lacked the required
// `box:` field destroyed the entire file's content (provides section,
// other deploy entries) and wrote only the new disposable: true marker.
func TestSaveDeployState_AbortOnInvalidExistingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-existing deploy.yml that fails spec.ValidateDeployRequiresBox —
	// `legacy-entry` is target:pod but lacks the required `box:`.
	initialYAML := `version: 2026.204.1223
provides:
    env:
        - name: SOME_URL
          value: http://example/api
          source: legacy-entry
deploy:
    legacy-entry:
        target: pod
    another-entry:
        target: pod
        box: another
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}
	initialBytes, _ := os.ReadFile(path)

	// Attempt to write the disposable flag for a brand-new entry, through the REAL
	// production seam (persistDeployState calls deploykit.SaveDeployState internally). With the
	// pre-fix code, this would call deploykit.LoadBundleConfig() → err → discarded → dc = empty
	// → entry.Disposable = true → SaveBundleConfig truncates the file. With the post-fix code,
	// the load error propagates and saveDeployState aborts before any write — the DISPATCH may
	// therefore surface an error too (tolerated here; the assertion that matters is byte-identity
	// below, not whether the dispatch call itself reports the abort).
	p := testSeamProvider(t)
	_ = testDispatchLifecycleAddTolerant(t, p, "newimage", spec.SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Box:           "newimage",
		Target:        "pod",
	})

	afterBytes, _ := os.ReadFile(path)
	if !bytes.Equal(initialBytes, afterBytes) {
		t.Errorf("saveDeployState mutated deploy.yml despite load-time validation error\n--- before ---\n%s\n--- after ---\n%s",
			initialBytes, afterBytes)
	}
}

// TestSaveDeployState_PersistsImageAndTargetForNewEntry pins the
// post-2026-05-16 require-image plumbing: when the caller passes
// Image/Target on a brand-new entry, both must land in deploy.yml
// alongside Disposable. Without this, the entry fails the require-image
// validator on the next load and bricks every subsequent `charly` invocation.
func TestSaveDeployState_PersistsImageAndTargetForNewEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initialYAML := `version: 2026.204.1223
existing-deploy:
    pod:
        image: existing-image
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	p := testSeamProvider(t)
	testDispatchLifecycleAdd(t, p, "newimage", spec.SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Box:           "newimage",
		Target:        "pod",
	})

	dc, err := testLoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	if dc == nil {
		t.Fatal("nil BundleConfig after reload")
	}

	if _, ok := dc.Bundle["existing-deploy"]; !ok {
		t.Error("existing-deploy entry was lost (merge failure)")
	}

	newEntry, ok := dc.Bundle["newimage"]
	if !ok {
		t.Fatal("newimage entry not added")
	}
	if newEntry.Image != "newimage" {
		t.Errorf("Image not persisted on new entry: got %q want %q", newEntry.Image, "newimage")
	}
	if newEntry.Target != "pod" {
		t.Errorf("Target not persisted on new entry: got %q want %q", newEntry.Target, "pod")
	}
	if newEntry.Disposable == nil || !*newEntry.Disposable {
		t.Error("Disposable not persisted on new entry")
	}
}

// TestSaveDeployState_DoesNotClobberExistingImageTarget pins the
// "only set when entry doesn't already declare" semantics: if a
// pre-existing entry already has box:/target:, a saveDeployState
// call with different Image/Target values MUST leave the existing
// values alone (operator authority over agent re-derivation).
func TestSaveDeployState_DoesNotClobberExistingImageTarget(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initialYAML := `version: 2026.204.1223
existing:
    pod:
        image: pinned-image-ref:1.2.3
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	p := testSeamProvider(t)
	testDispatchLifecycleAdd(t, p, "existing", spec.SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Box:           "would-clobber",
		Target:        "vm",
	})

	dc, err := testLoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	entry := dc.Bundle["existing"]
	if entry.Image != "pinned-image-ref:1.2.3" {
		t.Errorf("Image clobbered: got %q want %q", entry.Image, "pinned-image-ref:1.2.3")
	}
	if entry.Target != "pod" {
		t.Errorf("Target clobbered: got %q want %q", entry.Target, "pod")
	}
	if entry.Disposable == nil || !*entry.Disposable {
		t.Error("Disposable not applied (this field SHOULD update)")
	}
}

// TestRemoveVmDeployEntry_SelectiveAndIdempotent pins the deploy-lifecycle
// cleanup primitive that `charly vm destroy` (vm.go) and `charly bundle del vm:<name>`
// (the vm lifecycle hook's PostTeardown) rely on to remove a VM's deploy.yml entry on teardown
// — the inverse of the deploykit.SaveVmDeployState written on add (F6 vm-lifecycle move,
// coneB-vmlifecycle: the primitive relocated to deploykit.RemoveVmDeployEntry, invoked here with
// the same acquireDeployConfigLock + the test-local testSaveDeployConfig/testLoadBundleConfig
// callbacks that stand in for the deleted host save callback + DeployStateHost-backed
// nil reader, #55 coneC-dsh δ). It proves the two load-bearing
// properties of the fix:
//
//  1. SELECTIVE removal — removing `vm:k3s-vm` strips ONLY that entry; sibling
//     VM entries (incl. a running, preemptible operator workstation) and pod
//     entries survive untouched. This is the operator-safety property: a
//     disposable bed's teardown can never collateral-remove the workstation.
//  2. IDEMPOTENCY — a second removal of the already-gone entry returns nil and
//     leaves the file valid + siblings intact. This is the config-layer half of
//     the "a config whose libvirt domain is already destroyed is STILL cleaned"
//     behavior (the other half being vm.go's now-non-fatal lookupDomain miss).
//
// Without the fix, `charly vm destroy` never called RemoveVmDeployEntry, so a
// disposable check-bed VM entry lingered in deploy.yml after every bed run.
func TestRemoveVmDeployEntry_SelectiveAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Seed: the disposable bed VM to remove, plus a running preemptible
	// operator workstation and an unrelated pod deploy that must both survive.
	initialYAML := `version: 2026.204.1223
vm:k3s-vm:
    vm:
        from: k3s-vm
        vm_state:
            ssh_port: 38067
            ssh_user: arch
vm:cachyos-gpu:
    vm:
        from: cachyos-gpu
        preemptible:
            holds:
                - nvidia-gpu
web-app:
    pod:
        image: web-app
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// A REAL production teardown ("charly bundle del vm:k3s-vm") only reaches
	// deploykit.RemoveVmDeployEntry via handleDeployDel's OpPostTeardown leg, gated on a ledger
	// DeployRecord existing for the name (an unrecorded name is a no-op teardown, matching
	// production's own idempotent-absent-record contract) — so the seam-driven equivalent of
	// "this VM was deployed" is a real add-dispatch first (a benign no-op state patch: the
	// pre-seeded vm_state/ssh_port/ssh_user fields are untouched by SaveDeployStateInput, which
	// only ever sets Image/Target/Disposable).
	p := testSeamProvider(t)
	testDispatchLifecycleAdd(t, p, "vm:k3s-vm", spec.SaveDeployStateInput{})

	// (1) Selective removal of the disposable bed VM, through the REAL production seam
	// (dispatchDeployTarget("del") → handleDeployDel → OpPostTeardown → deploykit.RemoveVmDeployEntry).
	testDispatchLifecycleDel(t, p, "vm:k3s-vm", []string{"vm:k3s-vm"})
	dc, err := testLoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after removal: %v", err)
	}
	if _, ok := dc.LookupKey("vm:k3s-vm"); ok {
		t.Error("vm:k3s-vm still present after RemoveVmDeployEntry — entry not removed")
	}
	if _, ok := dc.LookupKey("vm:cachyos-gpu"); !ok {
		t.Error("vm:cachyos-gpu (operator workstation) was collateral-removed — selective-removal property violated")
	}
	if _, ok := dc.LookupKey("web-app"); !ok {
		t.Error("web-app pod deploy was collateral-removed — selective-removal property violated")
	}

	// (2) Idempotency: removing the already-gone entry is a clean no-op — the ledger record is
	// ALSO gone now (deleted alongside the deploy state by (1)'s teardown), so this re-exercises
	// handleDeployDel's own "nothing recorded — idempotent teardown" short-circuit (an even
	// stronger proof of the contract than calling RemoveVmDeployEntry a second time directly).
	testDispatchLifecycleDel(t, p, "vm:k3s-vm", []string{"vm:k3s-vm"})
	dc2, err := testLoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after idempotent re-removal: %v", err)
	}
	if _, ok := dc2.LookupKey("vm:cachyos-gpu"); !ok {
		t.Error("vm:cachyos-gpu disappeared after idempotent re-removal")
	}
	if _, ok := dc2.LookupKey("web-app"); !ok {
		t.Error("web-app disappeared after idempotent re-removal")
	}
}
