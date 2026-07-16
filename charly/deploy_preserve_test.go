package main

import (
	"github.com/opencharly/sdk/spec"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// TestCharlyUpdatePreservesPerHostDeployFields reproduces the operator's scenario
// through the ACTUAL `charly update <vm>` path: the vm lifecycle hook Rebuild shells
// `charly vm destroy` (removeVmDeployEntry) then `charly vm create` (saveVmDeployState).
// The per-host entry carries `preemptible` (a LOCAL deploy property) + env +
// tunnel; the destroy→create cycle must NOT clobber any of them. Against the
// pre-fix removeVmDeployEntry (which delete()d the whole entry) this FAILS —
// that was the root cause of the lost workstation preemptible.
func TestCharlyUpdatePreservesPerHostDeployFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "charly")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Per-host overlay keyed as `charly vm destroy`/`charly vm create` key it (vm:<name>):
	// preemptible + env + tunnel — operator-authored local state.
	yml := `version: 2026.186.2323
vm:cachyos-gpu:
    vm:
        from: cachyos-gpu
        vm_state:
            instance_id: original-uuid
            ssh_port: 2222
        preemptible:
            holds: [nvidia-gpu]
        env:
            EDITOR: nvim
        tunnel:
            provider: tailscale
            private: all
`
	if err := os.WriteFile(filepath.Join(cfgDir, "charly.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}

	// `charly update <vm>` == destroy (removeVmDeployEntry) THEN create
	// (saveVmDeployState), keyed on vm:<name>.
	if err := removeVmDeployEntry("vm:cachyos-gpu"); err != nil {
		t.Fatalf("removeVmDeployEntry (destroy leg): %v", err)
	}
	if err := saveVmDeployState("vm:cachyos-gpu", "cachyos-gpu", &spec.VmDeployState{InstanceID: "rebuilt-uuid", SshPort: 2222}); err != nil {
		t.Fatalf("saveVmDeployState (create leg): %v", err)
	}

	// Reload and assert NOTHING operator-authored was dropped by the cycle.
	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("LoadBundleConfig: %v", err)
	}
	node, ok := dc.Bundle["vm:cachyos-gpu"]
	if !ok {
		t.Fatal("vm:cachyos-gpu entry vanished after charly update destroy→create")
	}
	if node.Preemptible == nil || len(node.Preemptible.Holds) != 1 || node.Preemptible.Holds[0] != "nvidia-gpu" {
		t.Errorf("charly update DROPPED preemptible: got %+v", node.Preemptible)
	}
	if len(node.Env) != 1 || node.Env["EDITOR"] != "nvim" {
		t.Errorf("charly update DROPPED env: got %+v", node.Env)
	}
	if node.Tunnel == nil {
		t.Errorf("charly update DROPPED tunnel")
	}
	// And the rebuilt state must have landed.
	if node.VmState == nil || node.VmState.InstanceID != "rebuilt-uuid" {
		t.Errorf("vm_state not refreshed: got %+v", node.VmState)
	}
}

// TestVmDestroyRemovesPureAutoEntry guards the other half: a pure auto-created
// VM-state entry (target: vm + vm: + vm_state, NO operator config — e.g. a
// disposable check-bed VM) IS deleted on destroy, so such entries don't
// accumulate. This is why removeVmDeployEntry existed in the first place.
func TestVmDestroyRemovesPureAutoEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "charly")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := `version: 2026.186.2323
vm:check-cachyos-gpu-vm:
    vm:
        from: check-cachyos-gpu-vm
        vm_state:
            instance_id: bed-uuid
            ssh_port: 12227
`
	if err := os.WriteFile(filepath.Join(cfgDir, "charly.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeVmDeployEntry("vm:check-cachyos-gpu-vm"); err != nil {
		t.Fatalf("removeVmDeployEntry: %v", err)
	}
	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		t.Fatalf("LoadBundleConfig: %v", err)
	}
	if _, ok := dc.Bundle["vm:check-cachyos-gpu-vm"]; ok {
		t.Error("pure auto-created bed VM entry should be deleted on destroy (else entries accumulate)")
	}
}

// TestGatherDeployNodesPerHostWins proves the preempt arbiter's node view lets
// a PER-HOST preemptible (a local deploy property) override the committed
// project profile that lacks it — so `requires_exclusive` beds can preempt the
// operator's workstation only where the operator opted in, without committing
// the flag.
func TestGatherDeployNodesPerHostWins(t *testing.T) {
	proj := t.TempDir()
	// Committed project: cachyos-gpu, NO preemptible.
	projYml := `version: 2026.186.2323
cachyos-gpu:
    vm:
        from: cachyos-gpu
`
	if err := os.WriteFile(filepath.Join(proj, "charly.yml"), []byte(projYml), 0o644); err != nil {
		t.Fatal(err)
	}
	// Per-host overlay opts THIS host in.
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if err := os.MkdirAll(filepath.Join(cfg, "charly"), 0o755); err != nil {
		t.Fatal(err)
	}
	hostYml := `version: 2026.186.2323
cachyos-gpu:
    vm:
        from: cachyos-gpu
        preemptible:
            holds: [nvidia-gpu]
`
	if err := os.WriteFile(filepath.Join(cfg, "charly", "charly.yml"), []byte(hostYml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)

	nodes := gatherDeployNodes()
	node, ok := nodes["cachyos-gpu"]
	if !ok {
		t.Fatal("cachyos-gpu not gathered")
	}
	if node.Preemptible == nil || len(node.Preemptible.Holds) != 1 || node.Preemptible.Holds[0] != "nvidia-gpu" {
		t.Errorf("per-host preemptible did not win over committed project node: got %+v", node.Preemptible)
	}
	if node.From != "cachyos-gpu" { // committed field still present after the merge
		t.Errorf("committed vm field lost in merge: got %q", node.From)
	}
}

// TestMergeDeployConfigsPreservesPreemptible covers the project↔per-host overlay
// merge — the documented former drop-site for Disposable/Lifecycle. The
// committed project profile (no preemptible) merged with the per-host overlay
// (preemptible) must keep the per-host flag, regardless of merge order.
func TestMergeDeployConfigsPreservesPreemptible(t *testing.T) {
	project := &BundleConfig{Bundle: map[string]spec.BundleNode{
		"cachyos-gpu": {Target: "vm", From: "cachyos-gpu"}, // committed: NO preemptible
	}}
	perHost := &BundleConfig{Bundle: map[string]spec.BundleNode{
		"cachyos-gpu": {Preemptible: &spec.PreemptibleConfig{Holds: []string{"nvidia-gpu"}}}, // local opt-in
	}}
	for _, tc := range []struct {
		name    string
		configs []*BundleConfig
	}{
		{"project then per-host", []*BundleConfig{project, perHost}},
		{"per-host then project", []*BundleConfig{perHost, project}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merged := MergeDeployConfigs(tc.configs...)
			node := merged.Bundle["cachyos-gpu"]
			if node.Preemptible == nil || len(node.Preemptible.Holds) != 1 {
				t.Errorf("merge DROPPED per-host preemptible: got %+v", node.Preemptible)
			}
			if node.From != "cachyos-gpu" {
				t.Errorf("merge lost committed vm field: got %q", node.From)
			}
		})
	}
}
