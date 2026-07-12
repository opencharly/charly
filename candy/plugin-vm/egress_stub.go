package vm

// egress_stub.go wires the plugin's cloud-init egress validation to verb:egress. command:vm is
// COMPILED-IN now (P10), so the plugin reaches the egress provider (candy/plugin-egress, verb:egress)
// IN-PROC over the reverse channel — it no longer needs the host to run the validators for it. This
// SUPERSEDES the former out-of-process design where the plugin no-op'd egress and the host validated
// via the two-phase ValidateOnly create + the seed-ISO build. Both artifacts are now validated
// plugin-side: the libvirt domain XML via ValidateXMLEgress and the cloud-init seed via the
// vmshared.ValidateEgress hook below — both routed to verb:egress by vm_egress_shim.go's egressValidate.

// ValidateEgress validates a cloud-init artifact (the vmshared hook, called by RegenerateSeedISO →
// RenderCloudInit) against the egress schema via verb:egress. Wired onto vmshared.ValidateEgress in
// vmshared_aliases.go's init(). Best-effort graceful-degrade outside a command context (egressValidate).
func ValidateEgress(kind, label string, data []byte) error {
	return egressValidate(kind, label, "bytes", string(data))
}
