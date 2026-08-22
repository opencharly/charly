// CUE schema for the VM-CLIENT wire family (Cutover B unit 2, R-E4) — the host↔plugin-vm
// internal-op RPC types charly-core's thin dispatch wrapper (invokeVmPluginEnv,
// charly/vm_plugin_client.go) sends to candy/plugin-vm's verb:libvirt provider for
// domain-state/list-domains/resolve-spice/resolve-vnc/snapshot-internal ops (NOT
// authored config — never in #Node/#Op). These were HAND-WRITTEN Go structs in charly-core (an
// SDD violation — wire types are CUE-sourced without exception) mirrored by an independent
// hand-written twin in candy/plugin-vm (vm_target.go's DisplayEndpoint decoded the identical
// shape by field name). Single-sourcing here closes both: charly-core's decode structs AND
// candy/plugin-vm's DisplayEndpoint retype onto these generated defs.
//
// Package-less; concatenated into the spec compilation unit.

// #VmSnapshotCreateOpts parameterizes the creation of a snapshot — the host-resolved payload for
// the snapshot-internal "create"/"create-external" ops.
#VmSnapshotCreateOpts: {
	vm_name!:         string @go(VmName)
	snap_name!:       string @go(SnapName)
	mode?:            string @go(Mode)
	description?:     string @go(Description)
	quiesce?:         bool   @go(Quiesce)
	libvirt_backend?: string @go(LibvirtBackend)
}

// #VmSnapshotEntry is one snapshot record (mirrors sdk/vmshared's SnapshotEntry, the on-disk
// registry shape) — carried on delete/revert/promote ops so the plugin's go-libvirt call has the
// full record without re-reading the host-side registry itself.
#VmSnapshotEntry: {
	name!:         string @go(Name)
	mode!:         string @go(Mode)
	libvirt_name?: string @go(LibvirtName)
	disk_path?:    string @go(DiskPath)
	description?:  string @go(Description)
	created?:      string @go(Created)
	parent?:       string @go(Parent)
	refcount!:     int    @go(Refcount,type=int)
	quiesced?:     bool   @go(Quiesced)
}

// #VmSnapInternalReq is the snapshot-internal op payload (create/delete/revert/promote/
// create-external/delete-external/revert-external), threaded via #VmPluginEnv.snap.
#VmSnapInternalReq: {
	snap_op!:  string                  @go(SnapOp)
	vm_name!:  string                  @go(VmName)
	opts?:     #VmSnapshotCreateOpts   @go(Opts,optional=nillable)
	entry?:    #VmSnapshotEntry        @go(Entry,optional=nillable)
	out_path?: string                  @go(OutPath)
}

// #VmPluginEnv is the host→plugin env for an internal VM-resolution RPC (domain-state/
// list-domains/resolve-spice/resolve-vnc/snapshot-internal). Matches the SUBSET of
// candy/plugin-vm's own (richer) internal vmEnv decode struct that charly-core's dispatch
// actually sends — plugin-vm's vmEnv carries an ADDITIONAL `create` field (the `charly vm create`
// CLI's own in-process construction, never sent by charly-core), which stays a plugin-local
// concern outside this shared def (containing *VmSpec/VmRuntimeParams, neither CUE-sourced yet).
// (A former `state_dir` field served ONLY the "qemu-shutdown" op, whose sole sender —
// charly/vm_qemu_client.go — was deleted in FLOOR-SLIM-proper Unit-8B alongside the arbiter's
// direct-QEMU stop path; the field was removed in the same cutover, confirmed zero remaining
// setters via `git grep 'VmPluginEnv{'` across charly/candy/sdk.)
#VmPluginEnv: {
	vm_op!:       string             @go(VmOp)
	vm_name?:     string             @go(VmName)
	uri?:         string             @go(URI)
	force?:       bool               @go(Force)
	delete_disk?: bool               @go(DeleteDisk)
	snap?:        #VmSnapInternalReq @go(Snap,optional=nillable)
}

// #VmDisplayEndpoint describes how to reach one graphics channel (SPICE or VNC) of a running VM.
// Shared by candy/plugin-vm's own DisplayEndpoint (a running-VM's resolved endpoint, kind="spice"|
// "vnc") and charly-core's decode of the resolve-spice/resolve-vnc RPC reply — ONE shape, R3.
#VmDisplayEndpoint: {
	kind?:          string @go(Kind)
	is_socket?:     bool   @go(IsSocket)
	socket_path?:   string @go(SocketPath)
	host?:          string @go(Host)
	port?:          int    @go(Port,type=int)
	password?:      string @go(Password)
	tunnel_needed?: bool   @go(TunnelNeeded)
}

// #VmResolveResult decodes a resolve-spice/resolve-vnc reply.
#VmResolveResult: {
	endpoint?:      #VmDisplayEndpoint @go(Endpoint)
	error?:         string             @go(Error)
	tunnel_target?: string             @go(TunnelTarget)
}
