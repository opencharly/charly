package vm

// egress_stub.go holds the plugin's intentional egress no-op. The out-of-process plugin must NOT
// carry the egress subsystem (charly/egress.go, with vendored CUE schemas), so the HOST runs the
// real validators:
//   - cloud-init: egress-validated host-side when the host builds the seed ISO
//     (RegenerateSeedISO → RenderCloudInit → ValidateEgress, wired real in core's vmshared_aliases).
//   - libvirt domain XML: egress-validated host-side via the two-phase ValidateOnly create — the
//     plugin renders + RETURNS the XML, the host runs ValidateXMLEgress, then authorizes create
//     (charly/vm_create_spec.go runVmSpecCreate).
//
// vmshared's cloud-init generators call the vmshared.ValidateEgress hook; the plugin wires it to
// this no-op (vmshared_aliases.go) so any in-plugin call defers to the host instead of panicking on
// a nil hook. This is the FINAL design, not transitional.
func ValidateEgress(_ string, _ string, _ []byte) error { return nil }
