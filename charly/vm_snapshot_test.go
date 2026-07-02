package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSnapshotRegistry_RoundTrip writes a registry, reads it back, and
// confirms the contents match.
func TestSnapshotRegistry_RoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	vmName := "test-arch"
	reg := &SnapshotRegistry{
		Version: 1,
		Snapshots: map[string]*SnapshotEntry{
			"baseline": {
				Name:        "baseline",
				Mode:        "external",
				LibvirtName: "baseline",
				DiskPath:    "/tmp/disk.qcow2",
				Description: "fresh OS",
				Created:     "2026-04-29T10:00:00Z",
				Refcount:    0,
			},
			"checkpoint": {
				Name:     "checkpoint",
				Mode:     "internal",
				Created:  "2026-04-29T11:00:00Z",
				Refcount: 0,
			},
		},
	}
	if err := saveRegistry(vmName, reg); err != nil {
		t.Fatalf("saveRegistry: %v", err)
	}
	got, err := loadRegistry(vmName)
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	if len(got.Snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(got.Snapshots))
	}
	if got.Snapshots["baseline"].Mode != "external" {
		t.Errorf("baseline mode = %q, want external", got.Snapshots["baseline"].Mode)
	}
	if got.Snapshots["checkpoint"].Mode != "internal" {
		t.Errorf("checkpoint mode = %q, want internal", got.Snapshots["checkpoint"].Mode)
	}
}

// TestSnapshotRegistry_LoadEmpty returns a fresh empty registry when
// the file doesn't exist.
func TestSnapshotRegistry_LoadEmpty(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	reg, err := loadRegistry("nonexistent-vm")
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	if reg == nil || reg.Version != 1 {
		t.Errorf("expected fresh empty registry, got %+v", reg)
	}
	if len(reg.Snapshots) != 0 {
		t.Errorf("expected empty snapshots map, got %d entries", len(reg.Snapshots))
	}
}

// TestSnapshotRegistry_AtomicWrite verifies the write-temp + rename
// pattern doesn't leave a half-written file when Marshal succeeds.
func TestSnapshotRegistry_AtomicWrite(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	vmName := "test-vm"
	reg := &SnapshotRegistry{Snapshots: map[string]*SnapshotEntry{
		"a": {Name: "a", Mode: "external"},
	}}
	if err := saveRegistry(vmName, reg); err != nil {
		t.Fatalf("saveRegistry: %v", err)
	}
	path, _ := registryPath(vmName)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected registry file at %s, got %v", path, err)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Errorf("expected temp file to be cleaned up, but %s still exists", path+".tmp")
	}
}

// TestSnapshotRefcount_IncrementDecrement covers the refcount
// management used by clone / ephemeral instantiation.
func TestSnapshotRefcount_IncrementDecrement(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	vmName := "refcount-test"
	// Manually seed a snapshot entry without invoking the libvirt path.
	dir, err := snapshotsDir(vmName)
	if err != nil {
		t.Fatalf("snapshotsDir: %v", err)
	}
	reg := &SnapshotRegistry{Snapshots: map[string]*SnapshotEntry{
		"baseline": {Name: "baseline", Mode: "external", Refcount: 0},
	}}
	if err := saveRegistry(vmName, reg); err != nil {
		t.Fatalf("saveRegistry: %v", err)
	}

	if err := IncrementSnapshotRefcount(vmName, "baseline"); err != nil {
		t.Fatalf("Increment: %v", err)
	}
	if err := IncrementSnapshotRefcount(vmName, "baseline"); err != nil {
		t.Fatalf("Increment 2: %v", err)
	}
	got, _ := loadRegistry(vmName)
	if got.Snapshots["baseline"].Refcount != 2 {
		t.Errorf("after 2 increments, refcount = %d, want 2", got.Snapshots["baseline"].Refcount)
	}

	if err := DecrementSnapshotRefcount(vmName, "baseline"); err != nil {
		t.Fatalf("Decrement: %v", err)
	}
	got, _ = loadRegistry(vmName)
	if got.Snapshots["baseline"].Refcount != 1 {
		t.Errorf("after 1 decrement, refcount = %d, want 1", got.Snapshots["baseline"].Refcount)
	}

	// Unknown snapshot decrement is tolerant (logs and continues).
	if err := DecrementSnapshotRefcount(vmName, "nonexistent"); err != nil {
		t.Errorf("Decrement of nonexistent should be tolerant: %v", err)
	}

	// Unused for static-analysis-friendliness.
	_ = dir
}

// TestSnapshotDelete_RefuseWhenInUse verifies the gating check.
func TestSnapshotDelete_RefuseWhenInUse(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	vmName := "delete-refuse"
	reg := &SnapshotRegistry{Snapshots: map[string]*SnapshotEntry{
		"baseline": {Name: "baseline", Mode: "external", Refcount: 1},
	}}
	if err := saveRegistry(vmName, reg); err != nil {
		t.Fatalf("saveRegistry: %v", err)
	}

	err := DeleteSnapshot(SnapshotDeleteOpts{VmName: vmName, SnapName: "baseline"})
	if err == nil {
		t.Error("expected refusal when refcount > 0, got nil")
	}
}

// TestSnapshotRegistry_OnDiskFormat verifies the JSON shape stays
// stable for downstream consumers (deploy.yml mirror, status display).
func TestSnapshotRegistry_OnDiskFormat(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	vmName := "shape-test"
	reg := &SnapshotRegistry{
		Version: 1,
		Snapshots: map[string]*SnapshotEntry{
			"baseline": {
				Name:        "baseline",
				Mode:        "external",
				DiskPath:    "/tmp/disk.qcow2",
				Description: "test",
				Created:     "2026-04-29T10:00:00Z",
				Refcount:    1,
			},
		},
	}
	if err := saveRegistry(vmName, reg); err != nil {
		t.Fatalf("saveRegistry: %v", err)
	}

	path, _ := registryPath(vmName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["version"].(float64) != 1 {
		t.Errorf("version field missing or wrong: %v", parsed["version"])
	}
	if _, ok := parsed["snapshots"]; !ok {
		t.Error("snapshots field absent")
	}
	// Ensure the file lives at the expected path.
	want := filepath.Join(tmpHome, ".local", "share", "charly", "vm", "charly-"+vmName, "snapshots", "registry.json")
	if path != want {
		t.Errorf("registry path = %s, want %s", path, want)
	}
}
