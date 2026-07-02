package main

import (
	"fmt"
	"os"
	"text/tabwriter"
)

// vm_snapshot_cmd.go — Kong subcommand wiring for `charly vm snapshot {…}`.
// Wired into VmCmd via the Snapshot field (see vm.go).

// VmSnapshotCmd is the parent of `charly vm snapshot`.
type VmSnapshotCmd struct {
	Create  VmSnapshotCreateCmd  `cmd:"" help:"Create a snapshot of a VM (external by default; internal with --mode internal)"`
	List    VmSnapshotListCmd    `cmd:"" help:"List snapshots for a VM"`
	Delete  VmSnapshotDeleteCmd  `cmd:"" help:"Delete a snapshot (refuses while clones/ephemerals reference it)"`
	Revert  VmSnapshotRevertCmd  `cmd:"" help:"Revert a VM to a snapshot"`
	Promote VmSnapshotPromoteCmd `cmd:"" help:"Convert an internal snapshot to external mode (extracts via qemu-img convert)"`
}

// VmSnapshotCreateCmd implements `charly vm snapshot create <vm> <name>`.
type VmSnapshotCreateCmd struct {
	Vm          string `arg:"" help:"VM name (kind:vm entity)"`
	Name        string `arg:"" help:"Snapshot name"`
	Mode        string `long:"mode" enum:"external,internal" default:"external" help:"Snapshot mode: external (clone-friendly, separate file) or internal (disk-efficient, embedded)"`
	Description string `long:"description" help:"Human-facing description of the snapshot"`
	Quiesce     bool   `long:"quiesce" help:"Flush guest state via guest-agent fsfreeze before snapshotting (falls back to libvirt's plain freeze)"`
}

// Run executes `charly vm snapshot create`.
func (c *VmSnapshotCreateCmd) Run() error {
	entry, err := CreateSnapshot(SnapshotCreateOpts{
		VmName:      c.Vm,
		SnapName:    c.Name,
		Mode:        c.Mode,
		Description: c.Description,
		Quiesce:     c.Quiesce,
	})
	if err != nil {
		return err
	}
	fmt.Printf("created %s snapshot %q on vm %q\n", entry.Mode, entry.Name, c.Vm)
	if entry.DiskPath != "" {
		fmt.Printf("  disk: %s\n", entry.DiskPath)
	}
	return nil
}

// VmSnapshotListCmd implements `charly vm snapshot list <vm>`.
type VmSnapshotListCmd struct {
	Vm   string `arg:"" help:"VM name"`
	JSON bool   `long:"json" help:"Emit JSON instead of a table"`
}

func (c *VmSnapshotListCmd) Run() error {
	entries, err := ListSnapshots(c.Vm)
	if err != nil {
		return err
	}
	if c.JSON {
		return writeJSON(os.Stdout, entries)
	}
	if len(entries) == 0 {
		fmt.Printf("vm %q: no snapshots\n", c.Vm)
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tMODE\tCREATED\tREFCOUNT\tDESCRIPTION")
	for _, e := range entries {
		desc := e.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", e.Name, e.Mode, e.Created, e.Refcount, desc)
	}
	return tw.Flush()
}

// VmSnapshotDeleteCmd implements `charly vm snapshot delete <vm> <name>`.
type VmSnapshotDeleteCmd struct {
	Vm    string `arg:"" help:"VM name"`
	Name  string `arg:"" help:"Snapshot name"`
	Force bool   `long:"force" help:"Delete even when refcount > 0 (only safe after destroying consumers)"`
}

func (c *VmSnapshotDeleteCmd) Run() error {
	if err := DeleteSnapshot(SnapshotDeleteOpts{
		VmName:   c.Vm,
		SnapName: c.Name,
		Force:    c.Force,
	}); err != nil {
		return err
	}
	fmt.Printf("deleted snapshot %q on vm %q\n", c.Name, c.Vm)
	return nil
}

// VmSnapshotRevertCmd implements `charly vm snapshot revert <vm> <name>`.
type VmSnapshotRevertCmd struct {
	Vm   string `arg:"" help:"VM name"`
	Name string `arg:"" help:"Snapshot name"`
}

func (c *VmSnapshotRevertCmd) Run() error {
	if err := RevertSnapshot(c.Vm, c.Name); err != nil {
		return err
	}
	fmt.Printf("reverted vm %q to snapshot %q\n", c.Vm, c.Name)
	return nil
}

// VmSnapshotPromoteCmd implements `charly vm snapshot promote <vm> <name>`.
type VmSnapshotPromoteCmd struct {
	Vm   string `arg:"" help:"VM name"`
	Name string `arg:"" help:"Snapshot name (must be mode=internal)"`
}

func (c *VmSnapshotPromoteCmd) Run() error {
	entry, err := PromoteSnapshot(c.Vm, c.Name)
	if err != nil {
		return err
	}
	fmt.Printf("promoted snapshot %q on vm %q (now mode=external)\n", c.Name, c.Vm)
	if entry.DiskPath != "" {
		fmt.Printf("  disk: %s\n", entry.DiskPath)
	}
	return nil
}
