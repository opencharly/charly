package spec

// cloud_init_runtime_params.go — SPIKE: CloudInitRuntimeParams relocated from
// sdk/vmshared/cloud_init_render.go (#55 value-type relocation spike, cluster
// 4). A plain 4-field struct with zero methods and zero vmshared-only field
// types — moved verbatim. vmshared.CloudInitRuntimeParams becomes a type
// alias; RenderCloudInit (a genuine render FUNCTION — behavior) stays in
// vmshared unchanged, its signature untouched by the alias.

// CloudInitRuntimeParams carries the runtime-resolved state needed to
// render cloud-init user-data: the SSH public key to inject, the
// instance-id (stable UUIDv4 persisted in VmDeployState), the hostname,
// and whether cloud-init should inject the SSH key at all (computed
// from D13 auto-defaults + explicit VmKeyInjection overrides).
type CloudInitRuntimeParams struct {
	// SSHPublicKey is the OpenSSH authorized_keys-format public key
	// line (e.g. "ssh-ed25519 AAAA..."). Empty when key injection is
	// disabled or when VmSSH.KeySource == "none".
	SSHPublicKey string

	// InstanceID is the stable UUIDv4 cloud-init instance-id.
	// Pinned at first VM create and persisted in VmDeployState.
	InstanceID string

	// Hostname for the guest. Defaults to the VM name when empty.
	Hostname string

	// InjectKeyViaCloudInit is the resolved D13 key_injection.cloud_init
	// channel state. When false the renderer emits no
	// ssh_authorized_keys entries even if SSHPublicKey is populated.
	InjectKeyViaCloudInit bool
}
