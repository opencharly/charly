package fleet

// deploy_state_writer_test.go — relocated/split WRITER-BEHAVIOR half of the #55 final-tail
// deploy-state cluster (charly/deploy_save_test.go's remaining SaveFleetConfig tests,
// charly/deploy_preserve_test.go — team-lead directive, 2026-08-03 split-by-assertion round).
// Each test here asserts deploykit.SaveFleetConfig / SaveVmDeployState / RemoveVmDeployEntry's
// OWN contract (atomic-write properties, abort-on-unloadable-read, explicit-false round-trip,
// selective/idempotent removal, cross-call field preservation) using the SAME real writers this
// package's production code (config_cmd.go, deploy_target.go) calls. The charly-loader half of
// the original tests (does LoadUnified correctly parse the legacy-shape rejection / the
// Disposable *bool / a project's `vm: {from:...}` node) is either already covered generically by
// charly's own node_loader_test.go + wider VM test suite, or (Disposable *bool parsing) kept as
// its own narrow charly-side test reading a checked-in testdata fixture — see
// charly/fleetnode_disposable_loader_test.go.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"

	"github.com/opencharly/sdk/deploykit"
)

// TestSaveFleetConfig_AtomicWriteLeavesNoTempLeftover pins the tempfile + rename atomic-write
// guarantee: after a successful save, no .tmp leftovers remain and the file mode matches the
// original os.WriteFile(0600) contract.
func TestSaveFleetConfig_AtomicWriteLeavesNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dc := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"foo": {Target: "pod", Image: "foo"},
	}}
	if err := deploykit.SaveFleetConfig(dc, bedTestMarshalNode, bedTestLoadFleetConfig); err != nil {
		t.Fatalf("SaveFleetConfig: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "charly"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || (len(e.Name()) > 4 && e.Name()[:4] == ".dep") {
			if e.Name() != "charly.yml" {
				t.Errorf("leftover tempfile: %s", e.Name())
			}
		}
	}
	info, err := os.Stat(filepath.Join(dir, "charly", "charly.yml"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file mode = %o; want 0600", info.Mode().Perm())
	}
}

// TestSaveFleetConfig_RefusesToClobberUnloadableConfig pins the per-host persist fail-safe:
// when the caller's read callback reports the on-disk config currently FAILS to load, SaveFleetConfig
// MUST abort and leave the file byte-identical — never overwrite the recoverable bytes with a
// degraded/empty config. (The charly-side companion of this test additionally proved charly's
// real LoadUnified rejects a specific legacy `deploy:` shape — that claim is already covered
// generically by charly/node_loader_test.go, so it is not re-asserted here; this test isolates
// SaveFleetConfig's OWN abort-on-read-error contract with a literal erroring reader, exactly
// mirroring this package's sibling TestSaveFleetConfig_ErrorsWhenCallbackNil-style fixtures.)
func TestSaveFleetConfig_RefusesToClobberUnloadableConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "charly", "charly.yml")
	recoverable := []byte("version: 2026.225.1508\nweb:\n    pod:\n        image: web\n")
	if err := os.WriteFile(path, recoverable, 0o600); err != nil {
		t.Fatalf("write recoverable config: %v", err)
	}

	erroringRead := func() (*deploykit.FleetConfig, error) {
		return nil, errFixtureUnloadable
	}

	err := deploykit.SaveFleetConfig(&deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"new-entry": {Target: "pod", Image: "new-entry"},
	}}, bedTestMarshalNode, erroringRead)
	if err == nil {
		t.Fatal("SaveFleetConfig overwrote an unloadable config; expected a refuse-to-clobber error")
	}
	if !strings.Contains(err.Error(), "fails to load") {
		t.Errorf("error should explain the refusal to overwrite, got: %v", err)
	}

	afterBytes, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("config file went missing after refused write: %v", readErr)
	}
	if !bytes.Equal(recoverable, afterBytes) {
		t.Errorf("SaveFleetConfig mutated an unloadable config despite refusing\n--- before ---\n%s\n--- after ---\n%s", recoverable, afterBytes)
	}

	// Positive control: once the reader reports success again, a normal save proceeds — the
	// guard only blocks a currently-unloadable read.
	if err := deploykit.SaveFleetConfig(&deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"new-entry": {Target: "pod", Image: "new-entry"},
	}}, bedTestMarshalNode, bedTestLoadFleetConfig); err != nil {
		t.Fatalf("SaveFleetConfig with a healthy reader should succeed: %v", err)
	}
	dc, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("reload after clean save: %v", err)
	}
	if _, ok := dc.Fleet["new-entry"]; !ok {
		t.Errorf("clean save did not persist new-entry; got keys %v", fleetTestKeysOf(dc.Fleet))
	}
}

var errFixtureUnloadable = fixtureUnloadableErr{}

type fixtureUnloadableErr struct{}

func (fixtureUnloadableErr) Error() string {
	return "fixture: on-disk config fails to load (simulates a per-host migrate-path bug)"
}

// TestFleetNode_DisposableFalseRoundTrip_Writer pins the *bool Disposable WRITE-side fix: an
// operator's explicit `disposable: false` must survive re-marshal — with the prior
// `Disposable bool` + `omitempty` declaration, `false` was indistinguishable from "absent" at
// marshal time so the explicit lockdown intent was silently erased on the next save. (The
// charly-side companion test, charly/fleetnode_disposable_loader_test.go, proves the LOAD side:
// LoadUnified parsing nil/&false/&true correctly from a checked-in testdata fixture.)
func TestFleetNode_DisposableFalseRoundTrip_Writer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	locked, open := false, true
	dc := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"locked-pod": {Target: "pod", Image: "foo", Disposable: &locked},
		"open-pod":   {Target: "pod", Image: "bar", Disposable: &open},
		"bare-pod":   {Target: "pod", Image: "baz"},
	}}
	if err := deploykit.SaveFleetConfig(dc, bedTestMarshalNode, bedTestLoadFleetConfig); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(dir, "charly", "charly.yml"))
	if err != nil {
		t.Fatalf("read after save: %v", err)
	}
	if !bytes.Contains(out, []byte("disposable: false")) {
		t.Errorf("re-serialized charly.yml dropped explicit `disposable: false`:\n%s", string(out))
	}
	if !bytes.Contains(out, []byte("disposable: true")) {
		t.Errorf("re-serialized charly.yml dropped explicit `disposable: true`:\n%s", string(out))
	}
}

// TestCharlyUpdatePreservesPerHostDeployFields reproduces the operator's scenario through the
// destroy→create cycle `charly update <vm>` drives: RemoveVmDeployEntry (charly vm destroy) THEN
// SaveVmDeployState (charly vm create), both against the SAME per-host entry. The entry carries
// `preemptible` (a LOCAL deploy property) + env + tunnel — operator-authored local state — and
// the destroy→create cycle must NOT clobber any of them. Against the pre-fix RemoveVmDeployEntry
// (which delete()d the whole entry) this FAILS — that was the root cause of the lost workstation
// preemptible.
func TestCharlyUpdatePreservesPerHostDeployFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o755); err != nil {
		t.Fatal(err)
	}
	dc := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"vm:cachyos-gpu": {
			Target:      "vm",
			From:        "cachyos-gpu",
			VmState:     &spec.VmDeployState{InstanceID: "original-uuid", SSHPort: 2222},
			Preemptible: &spec.PreemptibleConfig{Holds: []string{"nvidia-gpu"}},
			Env:         map[string]string{"EDITOR": "nvim"},
			Tunnel:      &spec.TunnelYAML{},
		},
	}}
	if err := deploykit.SaveFleetConfig(dc, bedTestMarshalNode, bedTestLoadFleetConfig); err != nil {
		t.Fatalf("seed: %v", err)
	}

	save := func(d *deploykit.FleetConfig) error {
		return deploykit.SaveFleetConfig(d, bedTestMarshalNode, bedTestLoadFleetConfig)
	}
	// `charly update <vm>` == destroy (RemoveVmDeployEntry) THEN create (SaveVmDeployState).
	if err := deploykit.RemoveVmDeployEntry("vm:cachyos-gpu", save, bedTestLoadFleetConfig); err != nil {
		t.Fatalf("RemoveVmDeployEntry (destroy leg): %v", err)
	}
	if err := deploykit.SaveVmDeployState("vm:cachyos-gpu", "cachyos-gpu", &spec.VmDeployState{InstanceID: "rebuilt-uuid", SSHPort: 2222}, save, bedTestLoadFleetConfig); err != nil {
		t.Fatalf("SaveVmDeployState (create leg): %v", err)
	}

	dc2, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	node, ok := dc2.Fleet["vm:cachyos-gpu"]
	if !ok {
		t.Fatal("vm:cachyos-gpu entry vanished after destroy→create")
	}
	if node.Preemptible == nil || len(node.Preemptible.Holds) != 1 || node.Preemptible.Holds[0] != "nvidia-gpu" {
		t.Errorf("destroy→create DROPPED preemptible: got %+v", node.Preemptible)
	}
	if len(node.Env) != 1 || node.Env["EDITOR"] != "nvim" {
		t.Errorf("destroy→create DROPPED env: got %+v", node.Env)
	}
	if node.Tunnel == nil {
		t.Errorf("destroy→create DROPPED tunnel")
	}
	if node.VmState == nil || node.VmState.InstanceID != "rebuilt-uuid" {
		t.Errorf("vm_state not refreshed: got %+v", node.VmState)
	}
}

// TestVmDestroyRemovesPureAutoEntry guards the other half: a pure auto-created VM-state entry
// (target: vm + vm: + vm_state, NO operator config — e.g. a disposable check-bed VM) IS deleted
// on destroy, so such entries don't accumulate.
func TestVmDestroyRemovesPureAutoEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o755); err != nil {
		t.Fatal(err)
	}
	dc := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"vm:check-cachyos-gpu-vm": {
			Target:  "vm",
			From:    "check-cachyos-gpu-vm",
			VmState: &spec.VmDeployState{InstanceID: "bed-uuid", SSHPort: 12227},
		},
	}}
	if err := deploykit.SaveFleetConfig(dc, bedTestMarshalNode, bedTestLoadFleetConfig); err != nil {
		t.Fatalf("seed: %v", err)
	}
	save := func(d *deploykit.FleetConfig) error {
		return deploykit.SaveFleetConfig(d, bedTestMarshalNode, bedTestLoadFleetConfig)
	}
	if err := deploykit.RemoveVmDeployEntry("vm:check-cachyos-gpu-vm", save, bedTestLoadFleetConfig); err != nil {
		t.Fatalf("RemoveVmDeployEntry: %v", err)
	}
	dc2, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := dc2.Fleet["vm:check-cachyos-gpu-vm"]; ok {
		t.Error("pure auto-created bed VM entry should be deleted on destroy (else entries accumulate)")
	}
}

// TestSaveDeployState_PersistsImageAndTargetForNewEntry pins the post-2026-05-16
// require-image plumbing: when the caller passes Image/Target on a brand-new entry, both
// must land in deploy.yml alongside Disposable. Without this, the entry fails the
// require-image validator on the next load and bricks every subsequent `charly`
// invocation. Relocated from charly/deploy_save_test.go (the persistence-semantics half;
// the dispatch wiring stays in charly).
func TestSaveDeployState_PersistsImageAndTargetForNewEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initialYAML := `version: 2026.225.1508
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
	}, bedTestMarshalNode, bedTestLoadFleetConfig)

	dc, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	if dc == nil {
		t.Fatal("nil FleetConfig after reload")
	}
	if _, ok := dc.Fleet["existing-deploy"]; !ok {
		t.Error("existing-deploy entry was lost (merge failure)")
	}
	newEntry, ok := dc.Fleet["newimage"]
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

// TestSaveDeployState_DoesNotClobberExistingImageTarget pins the "only set when entry
// doesn't already declare" semantics: if a pre-existing entry already has box:/target:, a
// SaveDeployState call with different Image/Target values MUST leave the existing values
// alone (operator authority over agent re-derivation). Relocated from
// charly/deploy_save_test.go (the persistence-semantics half; the dispatch wiring stays
// in charly).
func TestSaveDeployState_DoesNotClobberExistingImageTarget(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initialYAML := `version: 2026.225.1508
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
	}, bedTestMarshalNode, bedTestLoadFleetConfig)

	dc, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	entry := dc.Fleet["existing"]
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

// TestRemoveVmDeployEntry_SelectiveAndIdempotent pins the two load-bearing properties of
// the deploy-lifecycle cleanup primitive that `charly vm destroy` and `charly fleet del
// vm:<name>` rely on:
//
//  1. SELECTIVE removal — removing `vm:k3s-vm` strips ONLY that entry; sibling VM entries
//     (incl. a running, preemptible operator workstation) and pod entries survive
//     untouched. This is the operator-safety property: a disposable bed's teardown can
//     never collateral-remove the workstation.
//  2. IDEMPOTENCY — a second removal of the already-gone entry returns nil and leaves the
//     file valid + siblings intact.
//
// Relocated from charly/deploy_save_test.go (the persistence-semantics half; the dispatch
// wiring stays in charly).
func TestRemoveVmDeployEntry_SelectiveAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Seed: the disposable bed VM to remove, plus a running preemptible
	// operator workstation and an unrelated pod deploy that must both survive.
	initialYAML := `version: 2026.225.1508
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

	save := func(dc *deploykit.FleetConfig) error {
		return deploykit.SaveFleetConfig(dc, bedTestMarshalNode, bedTestLoadFleetConfig)
	}
	// (1) Selective removal of the disposable bed VM.
	if err := deploykit.RemoveVmDeployEntry("vm:k3s-vm", save, bedTestLoadFleetConfig); err != nil {
		t.Fatalf("RemoveVmDeployEntry: %v", err)
	}
	dc, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("reload after removal: %v", err)
	}
	if _, ok := dc.Fleet["vm:k3s-vm"]; ok {
		t.Error("vm:k3s-vm still present after RemoveVmDeployEntry — entry not removed")
	}
	if _, ok := dc.Fleet["vm:cachyos-gpu"]; !ok {
		t.Error("vm:cachyos-gpu (operator workstation) was collateral-removed — selective-removal property violated")
	}
	if _, ok := dc.Fleet["web-app"]; !ok {
		t.Error("web-app pod deploy was collateral-removed — selective-removal property violated")
	}

	// (2) Idempotency: removing the already-gone entry is a clean no-op.
	if err := deploykit.RemoveVmDeployEntry("vm:k3s-vm", save, bedTestLoadFleetConfig); err != nil {
		t.Fatalf("idempotent re-removal: %v", err)
	}
	dc2, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("reload after idempotent re-removal: %v", err)
	}
	if _, ok := dc2.Fleet["vm:cachyos-gpu"]; !ok {
		t.Error("vm:cachyos-gpu disappeared after idempotent re-removal")
	}
	if _, ok := dc2.Fleet["web-app"]; !ok {
		t.Error("web-app disappeared after idempotent re-removal")
	}
}
