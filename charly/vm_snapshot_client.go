package main

import (
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// vm_snapshot_client.go — host-side RPC wrappers for the snapshot-internal ops. The go-libvirt
// snapshot impl (formerly vm_snapshot_internal.go) moved to candy/plugin-vm; vm_snapshot.go's
// orchestration (refcount/ledger, no go-libvirt) stays core and calls these wrappers, which RPC
// the out-of-process plugin.
//
// Cutover B unit 2 (R-E4): the wire payload is now spec.VmSnapInternalReq (CUE-sourced,
// sdk/schema/vmclient.cue) instead of a hand-written twin of candy/plugin-vm's own struct. The
// orchestration-facing SnapshotCreateOpts/SnapshotEntry (vmshared_aliases.go, the runtime/ledger
// shape vm_snapshot.go operates on) are DISTINCT from the wire's spec.VmSnapshotCreateOpts/
// spec.VmSnapshotEntry — toWireSnapshotOpts/toWireSnapshotEntry convert at the RPC boundary,
// mirroring how every other host→plugin call in this codebase keeps its runtime type separate
// from its wire type.

// toWireSnapshotOpts converts the runtime SnapshotCreateOpts (vmshared) into the wire
// spec.VmSnapshotCreateOpts sent to candy/plugin-vm.
func toWireSnapshotOpts(opts SnapshotCreateOpts) spec.VmSnapshotCreateOpts {
	return spec.VmSnapshotCreateOpts{
		VmName:         opts.VmName,
		SnapName:       opts.SnapName,
		Mode:           opts.Mode,
		Description:    opts.Description,
		Quiesce:        opts.Quiesce,
		LibvirtBackend: opts.LibvirtBackend,
	}
}

// toWireSnapshotEntry converts the runtime SnapshotEntry (vmshared, the on-disk registry record)
// into the wire spec.VmSnapshotEntry sent to candy/plugin-vm.
func toWireSnapshotEntry(entry *SnapshotEntry) *spec.VmSnapshotEntry {
	if entry == nil {
		return nil
	}
	return &spec.VmSnapshotEntry{
		Name:        entry.Name,
		Mode:        entry.Mode,
		LibvirtName: entry.LibvirtName,
		DiskPath:    entry.DiskPath,
		Description: entry.Description,
		Created:     entry.Created,
		Parent:      entry.Parent,
		Refcount:    entry.Refcount,
		Quiesced:    entry.Quiesced,
	}
}

func createInternalSnapshot(opts SnapshotCreateOpts) error {
	wireOpts := toWireSnapshotOpts(opts)
	return vmSnapInternal(spec.VmSnapInternalReq{SnapOp: "create", VmName: opts.VmName, Opts: &wireOpts})
}

func deleteInternalSnapshot(vmName string, entry *SnapshotEntry) error {
	return vmSnapInternal(spec.VmSnapInternalReq{SnapOp: "delete", VmName: vmName, Entry: toWireSnapshotEntry(entry)})
}

func revertInternalSnapshot(vmName string, entry *SnapshotEntry) error {
	return vmSnapInternal(spec.VmSnapInternalReq{SnapOp: "revert", VmName: vmName, Entry: toWireSnapshotEntry(entry)})
}

func promoteInternalToExternal(vmName string, entry *SnapshotEntry, outPath string) error {
	return vmSnapInternal(spec.VmSnapInternalReq{SnapOp: "promote", VmName: vmName, Entry: toWireSnapshotEntry(entry), OutPath: outPath})
}

func createExternalSnapshot(opts SnapshotCreateOpts, outFile string) error {
	wireOpts := toWireSnapshotOpts(opts)
	return vmSnapInternal(spec.VmSnapInternalReq{SnapOp: "create-external", VmName: opts.VmName, Opts: &wireOpts, OutPath: outFile})
}

func deleteExternalSnapshot(vmName string, entry *SnapshotEntry) error {
	return vmSnapInternal(spec.VmSnapInternalReq{SnapOp: "delete-external", VmName: vmName, Entry: toWireSnapshotEntry(entry)})
}

func revertExternalSnapshot(vmName string, entry *SnapshotEntry) error {
	return vmSnapInternal(spec.VmSnapInternalReq{SnapOp: "revert-external", VmName: vmName, Entry: toWireSnapshotEntry(entry)})
}

func vmSnapInternal(req spec.VmSnapInternalReq) error {
	raw, ok := invokeVmPluginEnv(spec.VmPluginEnv{VmOp: "snapshot-internal", Snap: &req})
	if !ok {
		return fmt.Errorf("vm plugin unavailable (go-libvirt snapshot is out-of-process)")
	}
	if e := vmPluginOpError(raw); e != "" {
		return fmt.Errorf("%s", e)
	}
	return nil
}
