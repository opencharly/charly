package main

import "fmt"

// vm_snapshot_client.go — host-side RPC wrappers for the snapshot-internal ops. The go-libvirt
// snapshot impl (formerly vm_snapshot_internal.go) moved to candy/plugin-vm; vm_snapshot.go's
// orchestration (refcount/ledger, no go-libvirt) stays core and calls these wrappers, which RPC
// the out-of-process plugin.

// vmSnapInternalReq is the snapshot-internal op payload (matches candy/plugin-vm's vmSnapInternalReq).
type vmSnapInternalReq struct {
	SnapOp  string              `json:"snap_op"`
	VmName  string              `json:"vm_name"`
	Opts    *SnapshotCreateOpts `json:"opts,omitempty"`
	Entry   *SnapshotEntry      `json:"entry,omitempty"`
	OutPath string              `json:"out_path,omitempty"`
}

func createInternalSnapshot(opts SnapshotCreateOpts) error {
	return vmSnapInternal(vmSnapInternalReq{SnapOp: "create", VmName: opts.VmName, Opts: &opts})
}

func deleteInternalSnapshot(vmName string, entry *SnapshotEntry) error {
	return vmSnapInternal(vmSnapInternalReq{SnapOp: "delete", VmName: vmName, Entry: entry})
}

func revertInternalSnapshot(vmName string, entry *SnapshotEntry) error {
	return vmSnapInternal(vmSnapInternalReq{SnapOp: "revert", VmName: vmName, Entry: entry})
}

func promoteInternalToExternal(vmName string, entry *SnapshotEntry, outPath string) error {
	return vmSnapInternal(vmSnapInternalReq{SnapOp: "promote", VmName: vmName, Entry: entry, OutPath: outPath})
}

func createExternalSnapshot(opts SnapshotCreateOpts, outFile string) error {
	return vmSnapInternal(vmSnapInternalReq{SnapOp: "create-external", VmName: opts.VmName, Opts: &opts, OutPath: outFile})
}

func deleteExternalSnapshot(vmName string, entry *SnapshotEntry) error {
	return vmSnapInternal(vmSnapInternalReq{SnapOp: "delete-external", VmName: vmName, Entry: entry})
}

func revertExternalSnapshot(vmName string, entry *SnapshotEntry) error {
	return vmSnapInternal(vmSnapInternalReq{SnapOp: "revert-external", VmName: vmName, Entry: entry})
}

func vmSnapInternal(req vmSnapInternalReq) error {
	raw, ok := invokeVmPluginEnv(vmPluginEnv{VmOp: "snapshot-internal", Snap: &req})
	if !ok {
		return fmt.Errorf("vm plugin unavailable (go-libvirt snapshot is out-of-process)")
	}
	if e := vmPluginOpError(raw); e != "" {
		return fmt.Errorf("%s", e)
	}
	return nil
}
