package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadVmInstanceOverride_Missing covers the common case: no
// override file → (nil, nil).
func TestLoadVmInstanceOverride_Missing(t *testing.T) {
	// Use a domain name very unlikely to exist in the test runner's
	// home dir. If it does exist, the test is invalid and we'd want
	// to know.
	domain := "test-instance-override-missing-domain-" + t.Name()
	got, err := LoadVmInstanceOverride(domain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing domain, got %v", got)
	}
}

// TestLoadVmInstanceOverride_Disposable covers the disposable: false
// override pulling from a known on-disk YAML.
func TestLoadVmInstanceOverride_Disposable(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	domain := "charly-archtest"
	dir := filepath.Join(tmpHome, ".local", "share", "charly", "vm", domain)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "instance.yml"), []byte("disposable: false\nlifecycle: long-running\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadVmInstanceOverride(domain)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil override")
	}
	if got.Disposable == nil {
		t.Fatal("expected Disposable to be non-nil pointer")
	}
	if *got.Disposable != false {
		t.Errorf("Disposable = %v, want false", *got.Disposable)
	}
	if got.Lifecycle != "long-running" {
		t.Errorf("Lifecycle = %q, want %q", got.Lifecycle, "long-running")
	}
}

// TestApplyToVmClassification covers all four merge branches.
func TestApplyToVmClassification(t *testing.T) {
	// nil override returns upstream verbatim
	{
		var charly *VmInstanceOverride
		d, life := charly.ApplyToVmClassification(true, "dev")
		if d != true || life != "dev" {
			t.Errorf("nil override: got (%v,%q), want (true,dev)", d, life)
		}
	}
	// empty override returns upstream verbatim
	{
		charly := &VmInstanceOverride{}
		d, life := charly.ApplyToVmClassification(true, "dev")
		if d != true || life != "dev" {
			t.Errorf("empty override: got (%v,%q), want (true,dev)", d, life)
		}
	}
	// disposable: false flips
	{
		f := false
		charly := &VmInstanceOverride{Disposable: &f}
		d, life := charly.ApplyToVmClassification(true, "dev")
		if d != false || life != "dev" {
			t.Errorf("disposable override: got (%v,%q), want (false,dev)", d, life)
		}
	}
	// lifecycle replaces
	{
		charly := &VmInstanceOverride{Lifecycle: "prod"}
		d, life := charly.ApplyToVmClassification(true, "dev")
		if d != true || life != "prod" {
			t.Errorf("lifecycle override: got (%v,%q), want (true,prod)", d, life)
		}
	}
	// both fields override
	{
		f := false
		charly := &VmInstanceOverride{Disposable: &f, Lifecycle: "prod"}
		d, life := charly.ApplyToVmClassification(true, "dev")
		if d != false || life != "prod" {
			t.Errorf("both override: got (%v,%q), want (false,prod)", d, life)
		}
	}
}

// TestApplyToVmSpec covers the per-host libvirt device overlay merge: nil
// override / nil overlay are no-ops, hostdevs + filesystems APPEND onto a
// spec (never replace), and a spec with no libvirt block at all receives the
// overlay (Libvirt + Devices created on demand).
func TestApplyToVmSpec(t *testing.T) {
	mkOverlay := func() *VmInstanceOverride {
		return &VmInstanceOverride{Libvirt: &LibvirtDomain{Devices: &LibvirtDevices{
			Hostdevs: []LibvirtHostdev{{
				Type: "pci", Managed: "yes",
				Source: map[string]string{"domain": "0x0000", "bus": "0x01", "slot": "0x00", "function": "0x0"},
			}},
			Filesystems: []LibvirtFilesystem{{
				Driver: "virtiofs", AccessMode: "passthrough", Source: "/home/op/work", Target: "workspace",
			}},
		}}}
	}

	// nil override → no-op (no panic, spec untouched).
	{
		var charly *VmInstanceOverride
		spec := &VmSpec{}
		charly.ApplyToVmSpec(spec)
		if spec.Libvirt != nil {
			t.Errorf("nil override should not create spec.Libvirt")
		}
	}
	// override with no libvirt overlay → no-op.
	{
		charly := &VmInstanceOverride{}
		spec := &VmSpec{}
		charly.ApplyToVmSpec(spec)
		if spec.Libvirt != nil {
			t.Errorf("empty override should not create spec.Libvirt")
		}
	}
	// spec with NO libvirt block → Libvirt + Devices created, overlay appended.
	{
		spec := &VmSpec{}
		mkOverlay().ApplyToVmSpec(spec)
		if spec.Libvirt == nil || spec.Libvirt.Devices == nil {
			t.Fatal("overlay should create spec.Libvirt.Devices")
		}
		if len(spec.Libvirt.Devices.Hostdevs) != 1 {
			t.Errorf("Hostdevs = %d, want 1", len(spec.Libvirt.Devices.Hostdevs))
		}
		if len(spec.Libvirt.Devices.Filesystems) != 1 {
			t.Errorf("Filesystems = %d, want 1", len(spec.Libvirt.Devices.Filesystems))
		}
		if got := spec.Libvirt.Devices.Hostdevs[0].Source["bus"]; got != "0x01" {
			t.Errorf("merged hostdev bus = %q, want 0x01", got)
		}
	}
	// spec with an EXISTING hostdev → overlay APPENDS (portable + host overlay coexist).
	{
		spec := &VmSpec{Libvirt: &LibvirtDomain{Devices: &LibvirtDevices{
			Hostdevs: []LibvirtHostdev{{Type: "pci", Source: map[string]string{"bus": "0x02"}}},
		}}}
		mkOverlay().ApplyToVmSpec(spec)
		if len(spec.Libvirt.Devices.Hostdevs) != 2 {
			t.Fatalf("Hostdevs = %d, want 2 (append, not replace)", len(spec.Libvirt.Devices.Hostdevs))
		}
		if spec.Libvirt.Devices.Hostdevs[0].Source["bus"] != "0x02" {
			t.Errorf("pre-existing hostdev must stay first")
		}
	}
}

// TestLoadVmInstanceOverride_LibvirtOverlay proves the on-disk instance.yml
// `libvirt:` block — the SAME shape an operator writes for a host GPU — parses
// into the overlay and survives a round trip through the loader.
func TestLoadVmInstanceOverride_LibvirtOverlay(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	domain := "charly-cachyos-gpu-vm"
	dir := filepath.Join(tmpHome, ".local", "share", "charly", "vm", domain)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := `libvirt:
  devices:
    hostdevs:
      - type: pci
        managed: "yes"
        source:
          domain: "0x0000"
          bus: "0x01"
          slot: "0x00"
          function: "0x0"
    filesystems:
      - driver: virtiofs
        accessmode: passthrough
        source: /home/user/.cache/charly/check-workspace
        target: workspace
`
	if err := os.WriteFile(filepath.Join(dir, "instance.yml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadVmInstanceOverride(domain)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Libvirt == nil || got.Libvirt.Devices == nil {
		t.Fatal("expected parsed libvirt overlay")
	}
	if n := len(got.Libvirt.Devices.Hostdevs); n != 1 {
		t.Fatalf("Hostdevs = %d, want 1", n)
	}
	if bus := got.Libvirt.Devices.Hostdevs[0].Source["bus"]; bus != "0x01" {
		t.Errorf("hostdev bus = %q, want 0x01", bus)
	}
	if fs := got.Libvirt.Devices.Filesystems; len(fs) != 1 || fs[0].Target != "workspace" {
		t.Errorf("filesystem overlay = %+v, want one share targeting workspace", fs)
	}

	// And it merges onto a portable spec.
	spec := &VmSpec{}
	got.ApplyToVmSpec(spec)
	if spec.Libvirt == nil || len(spec.Libvirt.Devices.Hostdevs) != 1 {
		t.Errorf("loaded overlay did not merge onto spec")
	}
}
