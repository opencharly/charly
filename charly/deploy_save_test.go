package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/deploykit"
)

// TestDeployConfigLookup_NilSafe pins the post-2026-05-16 cleanup of
// the call sites that previously wrote
//
//	dc := deploykit.LoadDeployConfigForRead("...")
//	if dc != nil {
//	    if entry, ok := dc.Deploy[deployKey(image, instance)]; ok { ... }
//	}
//
// using nil-safe Lookup/LookupKey methods. The contract: nil receiver
// returns (zero, false) so callers can chain
// `deploykit.LoadDeployConfigForRead(...).Lookup(image, instance)` without a
// separate nil check.
func TestDeployConfigLookup_NilSafe(t *testing.T) {
	var dc *deploykit.BundleConfig // nil
	if entry, ok := dc.Lookup("foo", ""); ok {
		t.Errorf("Lookup on nil dc returned ok=true entry=%+v; want (zero, false)", entry)
	}
	if entry, ok := dc.LookupKey("foo"); ok {
		t.Errorf("LookupKey on nil dc returned ok=true entry=%+v; want (zero, false)", entry)
	}
}

// TestDeployConfigLookup_PresentAndAbsent pins the basic Lookup
// contract: present entries return (entry, true); absent entries and
// nil deploy map return (zero, false). Instance form is keyed via
// deployKey (image/instance); LookupKey takes the raw deploy.yml key.
func TestDeployConfigLookup_PresentAndAbsent(t *testing.T) {
	dc := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		"foo":       {Target: "pod", Image: "foo"},
		"foo/inst1": {Target: "pod", Image: "foo"},
		"vm:arch":   {Target: "vm"},
	}}

	// Lookup (image, instance) form.
	if entry, ok := dc.Lookup("foo", ""); !ok || entry.Image != "foo" {
		t.Errorf("Lookup(foo, \"\") = (%+v, %v); want present", entry, ok)
	}
	if entry, ok := dc.Lookup("foo", "inst1"); !ok || entry.Image != "foo" {
		t.Errorf("Lookup(foo, inst1) = (%+v, %v); want present", entry, ok)
	}
	if entry, ok := dc.Lookup("missing", ""); ok {
		t.Errorf("Lookup(missing, \"\") = (%+v, %v); want absent", entry, ok)
	}

	// LookupKey (raw deploy.yml key) form.
	if entry, ok := dc.LookupKey("foo/inst1"); !ok || entry.Image != "foo" {
		t.Errorf("LookupKey(foo/inst1) = (%+v, %v); want present", entry, ok)
	}
	if entry, ok := dc.LookupKey("vm:arch"); !ok || entry.Target != "vm" {
		t.Errorf("LookupKey(vm:arch) = (%+v, %v); want present", entry, ok)
	}
	if entry, ok := dc.LookupKey("missing"); ok {
		t.Errorf("LookupKey(missing) = (%+v, %v); want absent", entry, ok)
	}

	// Empty / nil-map dc returns (zero, false).
	emptyDc := &deploykit.BundleConfig{}
	if entry, ok := emptyDc.Lookup("foo", ""); ok {
		t.Errorf("Lookup on empty dc returned ok=true entry=%+v", entry)
	}
}

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
	initialYAML := `version: 2026.202.0105
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

	// Attempt to write the disposable flag for a brand-new entry. With
	// the pre-fix code, this would call deploykit.LoadBundleConfig() → err →
	// discarded → dc = empty → entry.Disposable = true → SaveBundleConfig
	// truncates the file. With the post-fix code, the load error
	// propagates and saveDeployState aborts before any write.
	deploykit.SaveDeployState("newimage", "", deploykit.SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Box:           "newimage",
		Target:        "pod",
	}, marshalDeployNode)

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
	initialYAML := `version: 2026.202.0105
existing-deploy:
    pod:
        image: existing-image
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	deploykit.SaveDeployState("newimage", "", deploykit.SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Box:           "newimage",
		Target:        "pod",
	}, marshalDeployNode)

	dc, err := deploykit.LoadBundleConfig()
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
	initialYAML := `version: 2026.202.0105
existing:
    pod:
        image: pinned-image-ref:1.2.3
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	deploykit.SaveDeployState("existing", "", deploykit.SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Box:           "would-clobber",
		Target:        "vm",
	}, marshalDeployNode)

	dc, err := deploykit.LoadBundleConfig()
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

// TestSaveBundleConfig_AtomicWriteSurvivesIntermediateFailure pins the
// tempfile + rename atomic-write guarantee: if the marshal step succeeds
// but the rename step fails (simulated by making the target path a
// directory), the prior on-disk file MUST remain intact.
//
// We can't easily inject a failure into os.Rename in a unit test, so
// this test exercises the happy path's atomicity properties (file mode,
// no .tmp leftovers) as a regression guard for the implementation shape.
func TestSaveBundleConfig_AtomicWriteLeavesNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dc := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		"foo": {Target: "pod", Image: "foo"},
	}}
	if err := saveBundleConfigNodeForm(dc); err != nil {
		t.Fatalf("SaveBundleConfig: %v", err)
	}
	// No .tmp leftovers in the config dir.
	entries, err := os.ReadDir(filepath.Join(dir, "charly"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || (len(e.Name()) > 4 && e.Name()[:4] == ".dep") {
			if e.Name() != "deploy.yml" {
				t.Errorf("leftover tempfile: %s", e.Name())
			}
		}
	}
	// File mode is 0600 (matches the original os.WriteFile(0600) contract).
	info, err := os.Stat(filepath.Join(dir, "charly", "charly.yml"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file mode = %o; want 0600", info.Mode().Perm())
	}
}

// TestSaveBundleConfig_RefusesToClobberUnloadableConfig pins the per-host
// persist fail-safe: when the on-disk ~/.config/charly/charly.yml currently
// FAILS to load (e.g. the per-host migrate-path bug left it `version: <HEAD>` +
// a legacy `deploy:` map that the node-form loader gate rejects), SaveBundleConfig
// MUST abort with a `charly migrate` hint and leave the file byte-identical —
// never overwrite the recoverable bytes with a degraded/empty config.
//
// This is the single-point defense behind the read-degraded write-backs that go
// through loadDeployConfigForRead (config_image.go resolved-port / data-seeded /
// secret-migration) — those would otherwise hand SaveBundleConfig an empty
// BundleConfig and truncate the user's deploy state. It complements (does not
// replace) loadDeployConfigForWrite's primary abort-on-load-error gate.
func TestSaveBundleConfig_RefusesToClobberUnloadableConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A config the unified loader REJECTS: a legacy top-level `deploy:` map at
	// HEAD version (the exact shape the per-host migrate-path bug produced —
	// kit.ClassifyDoc hard-rejects it as legacy kind-keyed config).
	rejected := "version: " + LatestSchemaVersion().String() + "\n" +
		"provides:\n" +
		"    env:\n" +
		"        - name: SOME_URL\n" +
		"          value: http://example/api\n" +
		"          source: legacy\n" +
		"deploy:\n" +
		"    web:\n" +
		"        box: web\n" +
		"    api:\n" +
		"        box: api\n"
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(rejected), 0o600); err != nil {
		t.Fatalf("write rejected config: %v", err)
	}
	initialBytes, _ := os.ReadFile(path)

	// Sanity: the fixture really is rejected by the loader (otherwise the test
	// would pass vacuously).
	if _, lerr := deploykit.LoadBundleConfig(); lerr == nil {
		t.Fatal("fixture loaded cleanly; expected it to be rejected by the node-form gate")
	}

	// A write that would otherwise truncate must be REFUSED.
	err := saveBundleConfigNodeForm(&deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		"new-entry": {Target: "pod", Image: "new-entry"},
	}})
	if err == nil {
		t.Fatal("SaveBundleConfig overwrote an unloadable config; expected a refuse-to-clobber error")
	}
	if !strings.Contains(err.Error(), "fails to load") {
		t.Errorf("error should explain the refusal to overwrite, got: %v", err)
	}

	// The recoverable bytes are intact — nothing truncated, nothing deleted.
	afterBytes, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("config file went missing after refused write: %v", readErr)
	}
	if !bytes.Equal(initialBytes, afterBytes) {
		t.Errorf("SaveBundleConfig mutated an unloadable config despite refusing\n--- before ---\n%s\n--- after ---\n%s", initialBytes, afterBytes)
	}

	// Positive control: once the unloadable file is gone (here: removed, as the
	// migration would have replaced it with a clean node-form file), a normal
	// save proceeds — the guard only blocks a present-but-unloadable file.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := saveBundleConfigNodeForm(&deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		"new-entry": {Target: "pod", Image: "new-entry"},
	}}); err != nil {
		t.Fatalf("SaveBundleConfig on an absent file should succeed: %v", err)
	}
	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after clean save: %v", err)
	}
	if _, ok := dc.Bundle["new-entry"]; !ok {
		t.Errorf("clean save did not persist new-entry; got keys %v", bundleKeys(dc))
	}
}

// TestBundleNode_DisposableFalseRoundTrip pins the *bool Disposable
// fix: an operator's explicit `disposable: false` must survive YAML
// unmarshal → re-marshal. With the prior `Disposable bool` +
// `omitempty` declaration, `false` was indistinguishable from "absent"
// at marshal time so the explicit lockdown intent was silently erased
// on the next saveDeployState. With *bool, nil=absent and &false=
// explicit lockdown both round-trip faithfully.
func TestBundleNode_DisposableFalseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := `version: 2026.202.0105
locked-pod:
    pod:
        image: foo
        disposable: false
open-pod:
    pod:
        image: bar
        disposable: true
bare-pod:
    pod:
        image: baz
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if dc == nil {
		t.Fatal("LoadBundleConfig returned nil")
	}
	locked := dc.Bundle["locked-pod"]
	if locked.Disposable == nil {
		t.Fatal("locked-pod: explicit `disposable: false` parsed as nil; should be &false")
	}
	if *locked.Disposable {
		t.Errorf("locked-pod: disposable = %v, want false", *locked.Disposable)
	}
	if locked.IsDisposable() {
		t.Error("locked-pod.IsDisposable() returned true despite explicit disposable: false")
	}

	open := dc.Bundle["open-pod"]
	if open.Disposable == nil || !*open.Disposable {
		t.Errorf("open-pod: disposable = %v, want &true", open.Disposable)
	}
	if !open.IsDisposable() {
		t.Error("open-pod.IsDisposable() returned false despite explicit disposable: true")
	}

	bare := dc.Bundle["bare-pod"]
	if bare.Disposable != nil {
		t.Errorf("bare-pod: disposable = %v, want nil (field absent in source)", bare.Disposable)
	}
	if bare.IsDisposable() {
		t.Error("bare-pod.IsDisposable() returned true for absent disposable field")
	}

	if err := saveBundleConfigNodeForm(dc); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after save: %v", err)
	}
	if !bytes.Contains(out, []byte("disposable: false")) {
		t.Errorf("re-serialized deploy.yml dropped explicit `disposable: false`:\n%s", string(out))
	}
	if !bytes.Contains(out, []byte("disposable: true")) {
		t.Errorf("re-serialized deploy.yml dropped explicit `disposable: true`:\n%s", string(out))
	}
}

// TestRemoveVmDeployEntry_SelectiveAndIdempotent pins the deploy-lifecycle
// cleanup primitive that `charly vm destroy` (vm.go) and `charly bundle del vm:<name>`
// (the vm lifecycle hook's PostTeardown) rely on to remove a VM's deploy.yml entry on teardown
// — the inverse of the saveVmDeployState written on add. It proves the two
// load-bearing properties of the fix:
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
// Without the fix, `charly vm destroy` never called removeVmDeployEntry, so a
// disposable check-bed VM entry lingered in deploy.yml after every bed run.
func TestRemoveVmDeployEntry_SelectiveAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Seed: the disposable bed VM to remove, plus a running preemptible
	// operator workstation and an unrelated pod deploy that must both survive.
	initialYAML := `version: 2026.202.0105
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

	// (1) Selective removal of the disposable bed VM.
	if err := removeVmDeployEntry("vm:k3s-vm"); err != nil {
		t.Fatalf("removeVmDeployEntry(vm:k3s-vm): %v", err)
	}
	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after removal: %v", err)
	}
	if _, ok := dc.LookupKey("vm:k3s-vm"); ok {
		t.Error("vm:k3s-vm still present after removeVmDeployEntry — entry not removed")
	}
	if _, ok := dc.LookupKey("vm:cachyos-gpu"); !ok {
		t.Error("vm:cachyos-gpu (operator workstation) was collateral-removed — selective-removal property violated")
	}
	if _, ok := dc.LookupKey("web-app"); !ok {
		t.Error("web-app pod deploy was collateral-removed — selective-removal property violated")
	}

	// (2) Idempotency: removing the already-gone entry is a clean no-op.
	if err := removeVmDeployEntry("vm:k3s-vm"); err != nil {
		t.Fatalf("idempotent re-removal of vm:k3s-vm errored: %v", err)
	}
	dc2, err := deploykit.LoadBundleConfig()
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
