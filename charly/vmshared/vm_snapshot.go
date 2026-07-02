package vmshared

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// vm_snapshot.go — declarative snapshot orchestration. Holds the
// per-VM unified registry.json (single source of truth), refcount
// management, and the mode-aware dispatch into vm_snapshot_libvirt.go
// (external mode) and vm_snapshot_internal.go (internal mode).
//
// Storage layout, per VM:
//
//   ~/.local/share/charly/vm/charly-<vm>/
//   ├── disk.qcow2                          # primary; also holds internal snapshots
//   └── snapshots/
//       ├── registry.json                   # ALL snapshots (internal + external)
//       └── <name>/                         # external mode only
//           ├── disk.qcow2
//           └── meta.json                   # description / created / parent / refcount
//
// The registry is the source of truth. The per-directory meta.json is a
// self-describing fallback so a manual disk inspection still makes sense
// if the registry desyncs.

// SnapshotRegistry is the on-disk schema for snapshots/registry.json.
// Versioned so future shape evolutions can migrate cleanly.
type SnapshotRegistry struct {
	// Version is the registry schema version. V1 is the initial release.
	Version int `json:"version"`

	// Snapshots is the unified set of snapshots known to charly for this
	// VM, keyed by Name. Both modes appear here.
	Snapshots map[string]*SnapshotEntry `json:"snapshots"`
}

// SnapshotEntry is one snapshot record. Mirrors VmSnapshotState plus
// on-disk-only fields (the registry is internal; VmSnapshotState is the
// charly.yml-facing mirror).
type SnapshotEntry struct {
	// Name uniquely identifies the snapshot within this VM.
	Name string `json:"name"`

	// Mode is "external" or "internal".
	Mode string `json:"mode"`

	// LibvirtName is the snapshot's name as known to libvirt. For
	// external mode, libvirt registers the snapshot as a domain
	// snapshot and we store the libvirt-side identifier here. For
	// internal mode, this matches Name (qemu-img embeds the literal
	// name).
	LibvirtName string `json:"libvirt_name,omitempty"`

	// DiskPath is the absolute path to the external snapshot file.
	// Empty for internal-mode snapshots.
	DiskPath string `json:"disk_path,omitempty"`

	// Description carries the operator-supplied note.
	Description string `json:"description,omitempty"`

	// Created is the RFC3339 creation timestamp.
	Created string `json:"created,omitempty"`

	// Parent is the prior snapshot in the implicit chain at create
	// time (whichever was current then). Informational; helps trace
	// backing-chain ancestry.
	Parent string `json:"parent,omitempty"`

	// Refcount tracks active clones / ephemerals depending on this
	// snapshot. delete refuses while > 0.
	Refcount int `json:"refcount"`

	// Quiesced records whether the snapshot was taken with guest-agent
	// fsfreeze active. Informational; helps an operator decide
	// whether the snapshot is consistent.
	Quiesced bool `json:"quiesced,omitempty"`
}

// snapshotsDir returns the absolute path to the snapshots/ directory
// for a given VM. Creates intermediate directories on demand.
func snapshotsDir(vmName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".local", "share", "charly", "vm", "charly-"+vmName, "snapshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating snapshots dir %s: %w", dir, err)
	}
	return dir, nil
}

// vmDiskPath returns the absolute path to the VM's primary qcow2 disk
// (the file that holds internal snapshots and that external snapshots
// back onto). For charly-built VMs this is output/qcow2/disk.qcow2 in the
// project tree; for adopted (imported) VMs this is the path recorded in
// VmSource.DiskPath.
//
// V1 returns a best-effort guess: project-relative output path if it
// exists, otherwise empty. Callers that need authoritative resolution
// (for clone backing, for libvirt snapshot XML) should pass an
// explicit override; this helper is for the registry's own bookkeeping.
func vmDiskPath(vmName string) (string, error) {
	// Per-VM disk dir used by the charly vm build cloud_image / bootc / bootstrap
	// paths. (See charly/vm_create_spec.go which resolves the same per-VM
	// output/qcow2/<vm>/disk.qcow2.)
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(cwd, VmDiskDir(vmName), "disk.qcow2")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	// Fall back to the VM state dir (some adoption flows symlink here).
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	candidate = filepath.Join(home, ".local", "share", "charly", "vm", "charly-"+vmName, "disk.qcow2")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("vm %q: cannot locate primary disk (looked in %s/disk.qcow2 and ~/.local/share/charly/vm/charly-%s/disk.qcow2)", vmName, VmDiskDir(vmName), vmName)
}

// registryPath returns the registry.json path for a VM.
func registryPath(vmName string) (string, error) {
	dir, err := snapshotsDir(vmName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "registry.json"), nil
}

// loadRegistry reads registry.json or returns an empty registry if the
// file doesn't exist. The empty-default behavior makes the first-snapshot
// flow a single write rather than two.
func loadRegistry(vmName string) (*SnapshotRegistry, error) {
	path, err := registryPath(vmName)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SnapshotRegistry{Version: 1, Snapshots: map[string]*SnapshotEntry{}}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var reg SnapshotRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if reg.Snapshots == nil {
		reg.Snapshots = map[string]*SnapshotEntry{}
	}
	if reg.Version == 0 {
		reg.Version = 1
	}
	return &reg, nil
}

// saveRegistry atomically writes the registry to disk (write-temp +
// rename pattern so a crash mid-write doesn't truncate the file).
func saveRegistry(vmName string, reg *SnapshotRegistry) error {
	path, err := registryPath(vmName)
	if err != nil {
		return err
	}
	if reg.Snapshots == nil {
		reg.Snapshots = map[string]*SnapshotEntry{}
	}
	if reg.Version == 0 {
		reg.Version = 1
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling registry: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming %s → %s: %w", tmp, path, err)
	}
	return nil
}

// snapshotMetaPath returns the per-snapshot meta.json sidecar path.
// External-mode only.
func snapshotMetaPath(vmName, snapName string) (string, error) {
	dir, err := snapshotsDir(vmName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, snapName, "meta.json"), nil
}

// snapshotExternalDiskPath returns the absolute path to the external
// snapshot's qcow2 file. Caller is expected to MkdirAll the parent
// before write.
func snapshotExternalDiskPath(vmName, snapName string) (string, error) {
	dir, err := snapshotsDir(vmName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, snapName, "disk.qcow2"), nil
}

// SnapshotCreateOpts parameterizes the creation of a snapshot.
type SnapshotCreateOpts struct {
	// VmName is the kind:vm entity name (without charly- prefix).
	VmName string

	// SnapName is the new snapshot's name.
	SnapName string

	// Mode is "external" or "internal" — empty defaults to external.
	Mode string

	// Description is an optional human note.
	Description string

	// Quiesce, when true, requests guest-agent fsfreeze before
	// snapshotting (with libvirt's plain freeze as fallback).
	Quiesce bool

	// LibvirtBackend, when non-nil, overrides the auto-detected backend.
	// Default: probe via existing resolveVmBackend.
	LibvirtBackend string
}

// CreateSnapshot is the mode-aware orchestrator for `charly vm snapshot
// create`. Looks up the active VM, dispatches to the matching mode-
// specific implementation, and records the result in registry.json +
// meta.json.
func CreateSnapshot(opts SnapshotCreateOpts) (*SnapshotEntry, error) {
	if opts.VmName == "" {
		return nil, fmt.Errorf("CreateSnapshot: vm name is required")
	}
	if opts.SnapName == "" {
		return nil, fmt.Errorf("CreateSnapshot: snapshot name is required")
	}
	mode := opts.Mode
	if mode == "" {
		mode = "external"
	}
	if mode != "external" && mode != "internal" {
		return nil, fmt.Errorf("CreateSnapshot: unknown mode %q (want external or internal)", mode)
	}

	reg, err := loadRegistry(opts.VmName)
	if err != nil {
		return nil, err
	}
	if _, exists := reg.Snapshots[opts.SnapName]; exists {
		return nil, fmt.Errorf("vm %q: snapshot %q already exists", opts.VmName, opts.SnapName)
	}

	// Implicit parent = whichever snapshot was most recently created
	// (the head of the chain). V1 picks the lexicographically last
	// matching mode; V2 will track an explicit "current" head.
	parent := implicitParent(reg)

	created := time.Now().UTC().Format(time.RFC3339)
	entry := &SnapshotEntry{
		Name:        opts.SnapName,
		Mode:        mode,
		LibvirtName: opts.SnapName,
		Description: opts.Description,
		Created:     created,
		Parent:      parent,
		Quiesced:    opts.Quiesce,
		Refcount:    0,
	}

	switch mode {
	case "external":
		diskPath, err := snapshotExternalDiskPath(opts.VmName, opts.SnapName)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(diskPath), 0o755); err != nil {
			return nil, fmt.Errorf("creating snapshot dir: %w", err)
		}
		if err := CreateExternalSnapshot(opts, diskPath); err != nil {
			return nil, fmt.Errorf("vm %q: external snapshot %q: %w", opts.VmName, opts.SnapName, err)
		}
		entry.DiskPath = diskPath
	case "internal":
		if err := CreateInternalSnapshot(opts); err != nil {
			return nil, fmt.Errorf("vm %q: internal snapshot %q: %w", opts.VmName, opts.SnapName, err)
		}
	}

	reg.Snapshots[opts.SnapName] = entry
	if err := saveRegistry(opts.VmName, reg); err != nil {
		return nil, err
	}
	if mode == "external" {
		if err := writeSnapshotMeta(opts.VmName, opts.SnapName, entry); err != nil {
			return nil, err
		}
	}
	return entry, nil
}

// ListSnapshots returns the snapshots for a VM as a name-sorted slice.
func ListSnapshots(vmName string) ([]*SnapshotEntry, error) {
	reg, err := loadRegistry(vmName)
	if err != nil {
		return nil, err
	}
	out := make([]*SnapshotEntry, 0, len(reg.Snapshots))
	for _, e := range reg.Snapshots {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SnapshotDeleteOpts parameterizes deletion.
type SnapshotDeleteOpts struct {
	VmName   string
	SnapName string
	// Force allows deletion even when refcount > 0. Default false.
	// Recommended only when the consuming clones/ephemerals have
	// already been destroyed and the registry is stale.
	Force bool
}

// DeleteSnapshot is the mode-aware deletion. Refuses while refcount > 0
// unless Force is set.
func DeleteSnapshot(opts SnapshotDeleteOpts) error {
	reg, err := loadRegistry(opts.VmName)
	if err != nil {
		return err
	}
	entry, ok := reg.Snapshots[opts.SnapName]
	if !ok {
		return fmt.Errorf("vm %q: snapshot %q does not exist", opts.VmName, opts.SnapName)
	}
	if entry.Refcount > 0 && !opts.Force {
		return fmt.Errorf("vm %q: snapshot %q has refcount=%d (clones/ephemerals depend on it); pass --force only after destroying them",
			opts.VmName, opts.SnapName, entry.Refcount)
	}

	switch entry.Mode {
	case "external":
		if err := DeleteExternalSnapshot(opts.VmName, entry); err != nil {
			return fmt.Errorf("vm %q: external snapshot %q: %w", opts.VmName, opts.SnapName, err)
		}
		// Remove the per-snapshot directory + meta.json.
		dir, derr := snapshotsDir(opts.VmName)
		if derr == nil {
			_ = os.RemoveAll(filepath.Join(dir, opts.SnapName))
		}
	case "internal":
		if err := DeleteInternalSnapshot(opts.VmName, entry); err != nil {
			return fmt.Errorf("vm %q: internal snapshot %q: %w", opts.VmName, opts.SnapName, err)
		}
	default:
		return fmt.Errorf("vm %q: snapshot %q has unknown mode %q", opts.VmName, opts.SnapName, entry.Mode)
	}

	delete(reg.Snapshots, opts.SnapName)
	return saveRegistry(opts.VmName, reg)
}

// RevertSnapshot is the mode-aware revert.
func RevertSnapshot(vmName, snapName string) error {
	reg, err := loadRegistry(vmName)
	if err != nil {
		return err
	}
	entry, ok := reg.Snapshots[snapName]
	if !ok {
		return fmt.Errorf("vm %q: snapshot %q does not exist", vmName, snapName)
	}
	switch entry.Mode {
	case "external":
		return RevertExternalSnapshot(vmName, entry)
	case "internal":
		return RevertInternalSnapshot(vmName, entry)
	default:
		return fmt.Errorf("vm %q: snapshot %q has unknown mode %q", vmName, snapName, entry.Mode)
	}
}

// PromoteSnapshot converts an internal snapshot to external mode by
// extracting it via `qemu-img convert` to a new qcow2 file in the
// snapshots directory. After promotion, the snapshot is usable as a
// clone backing target. The internal snapshot inside the primary qcow2
// is left in place — promote is non-destructive.
func PromoteSnapshot(vmName, snapName string) (*SnapshotEntry, error) {
	reg, err := loadRegistry(vmName)
	if err != nil {
		return nil, err
	}
	entry, ok := reg.Snapshots[snapName]
	if !ok {
		return nil, fmt.Errorf("vm %q: snapshot %q does not exist", vmName, snapName)
	}
	if entry.Mode != "internal" {
		return nil, fmt.Errorf("vm %q: snapshot %q is already mode=%q (only internal snapshots are promotable)", vmName, snapName, entry.Mode)
	}

	diskPath, err := snapshotExternalDiskPath(vmName, snapName)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(diskPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating snapshot dir: %w", err)
	}
	if err := PromoteInternalToExternal(vmName, entry, diskPath); err != nil {
		return nil, fmt.Errorf("vm %q: promoting snapshot %q: %w", vmName, snapName, err)
	}
	entry.Mode = "external"
	entry.DiskPath = diskPath
	if err := saveRegistry(vmName, reg); err != nil {
		return nil, err
	}
	if err := writeSnapshotMeta(vmName, snapName, entry); err != nil {
		return nil, err
	}
	return entry, nil
}

// IncrementSnapshotRefcount increases the refcount on the named
// snapshot. Used by clone/ephemeral instantiation paths.
func IncrementSnapshotRefcount(vmName, snapName string) error {
	reg, err := loadRegistry(vmName)
	if err != nil {
		return err
	}
	entry, ok := reg.Snapshots[snapName]
	if !ok {
		return fmt.Errorf("vm %q: snapshot %q does not exist (cannot reference)", vmName, snapName)
	}
	entry.Refcount++
	if err := saveRegistry(vmName, reg); err != nil {
		return err
	}
	if entry.Mode == "external" {
		_ = writeSnapshotMeta(vmName, snapName, entry)
	}
	return nil
}

// DecrementSnapshotRefcount decreases the refcount. Floors at 0.
func DecrementSnapshotRefcount(vmName, snapName string) error {
	reg, err := loadRegistry(vmName)
	if err != nil {
		return err
	}
	entry, ok := reg.Snapshots[snapName]
	if !ok {
		// Tolerant: a snapshot that's gone (manually removed) shouldn't
		// block ephemeral teardown. Log-and-continue.
		fmt.Fprintf(os.Stderr, "warning: vm %q snapshot %q absent during refcount decrement (already deleted?)\n", vmName, snapName)
		return nil
	}
	if entry.Refcount > 0 {
		entry.Refcount--
	}
	if err := saveRegistry(vmName, reg); err != nil {
		return err
	}
	if entry.Mode == "external" {
		_ = writeSnapshotMeta(vmName, snapName, entry)
	}
	return nil
}

// LookupSnapshot returns a snapshot entry by name or an error.
func LookupSnapshot(vmName, snapName string) (*SnapshotEntry, error) {
	reg, err := loadRegistry(vmName)
	if err != nil {
		return nil, err
	}
	entry, ok := reg.Snapshots[snapName]
	if !ok {
		return nil, fmt.Errorf("vm %q: snapshot %q does not exist; create with: charly vm snapshot create %s %s", vmName, snapName, vmName, snapName)
	}
	return entry, nil
}

// MirrorSnapshotsToDeployState copies the registry into a slice of
// VmSnapshotState records suitable for embedding in charly.yml's
// vm_state. Sorted by name for stable diffs.
func MirrorSnapshotsToDeployState(vmName string) ([]VmSnapshotState, error) {
	entries, err := ListSnapshots(vmName)
	if err != nil {
		return nil, err
	}
	out := make([]VmSnapshotState, 0, len(entries))
	for _, e := range entries {
		out = append(out, VmSnapshotState{
			Name:        e.Name,
			Mode:        e.Mode,
			LibvirtName: e.LibvirtName,
			DiskPath:    e.DiskPath,
			Description: e.Description,
			Created:     e.Created,
			Parent:      e.Parent,
			Refcount:    e.Refcount,
		})
	}
	return out, nil
}

// implicitParent returns the most-recently-created snapshot name in the
// registry, or empty if there are none. Used for implicit chain
// tracking at create-time (V1 doesn't honor explicit From: yet).
func implicitParent(reg *SnapshotRegistry) string {
	var newest string
	var newestTime time.Time
	for name, e := range reg.Snapshots {
		t, err := time.Parse(time.RFC3339, e.Created)
		if err != nil {
			continue
		}
		if newest == "" || t.After(newestTime) {
			newest = name
			newestTime = t
		}
	}
	return newest
}

// writeSnapshotMeta emits the per-snapshot meta.json sidecar for
// external-mode snapshots. Internal-mode snapshots have no sidecar.
func writeSnapshotMeta(vmName, snapName string, entry *SnapshotEntry) error {
	if entry.Mode != "external" {
		return nil
	}
	path, err := snapshotMetaPath(vmName, snapName)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// SnapshotsRefcountSummary returns "<n> active references" or empty
// when zero — for human-readable status output.
func SnapshotsRefcountSummary(vmName string) string {
	entries, err := ListSnapshots(vmName)
	if err != nil {
		return ""
	}
	var refs []string
	for _, e := range entries {
		if e.Refcount > 0 {
			refs = append(refs, fmt.Sprintf("%s=%d", e.Name, e.Refcount))
		}
	}
	if len(refs) == 0 {
		return ""
	}
	return strings.Join(refs, ", ")
}
