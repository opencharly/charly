package main

import "testing"

// provider_kind_invoke_concrete_test.go is the INTEGRATION-level end-to-end proof that the vm
// PCI-hostdev concreteness gap is closed — through the REAL production pipeline
// (validateProjectForBuild → LoadUnified → foldSubstrateKind → dispatchKindOpValidate →
// candy/plugin-substrate's compiled-in "vm" capability OpValidate handler, validate_vm.go), not
// a direct unit call into a host-side function. The check itself moved OUT of charly/ core (see
// validateKindValueCUE's comment for the architecture-review history); its FOCUSED unit tests
// now live beside the implementation in candy/plugin-substrate/validate_vm_test.go. These
// fixtures prove the WIRING — the host actually dispatches OpValidate for "vm" and surfaces the
// plugin's error-severity Diagnostics as a load failure — using the exact fixture shapes the
// former host-side unit tests used, so no coverage was lost in the move.

// TestValidate_VmPCIHostdev_MissingSlotFunction_Rejected: a `vm:` libvirt PCI hostdev whose
// slot/function were never authored MUST fail `charly box validate` (the real production load
// gate) — it silently passed (exit 0) before the validation-correctness fix.
func TestValidate_VmPCIHostdev_MissingSlotFunction_Rejected(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.204.1223
myvm:
  vm:
    source:
      kind: cloud_image
      url: http://x
    libvirt:
      devices:
        hostdevs:
        - type: pci
          source:
            domain: "0x0000"
            bus: "0x01"
`,
	})
	mustValidateErr(t, dir, `libvirt.devices.hostdevs[0].source.slot`, "must be a concrete PCI hex address")
}

// TestValidate_VmPCIHostdev_Complete_Accepted proves the fix is narrowly scoped: a
// FULLY-specified PCI hostdev still validates clean.
func TestValidate_VmPCIHostdev_Complete_Accepted(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.204.1223
myvm:
  vm:
    source:
      kind: cloud_image
      url: http://x
    libvirt:
      devices:
        hostdevs:
        - type: pci
          source:
            domain: "0x0000"
            bus: "0x01"
            slot: "0x00"
            function: "0x0"
`,
	})
	mustValidateOK(t, dir)
}

// TestValidate_VmNonPCIHostdev_Incomplete_Accepted proves the check is scoped to PCI hostdevs
// only (per the schema's own `if type == "pci"` conditional requirement) — a usb hostdev with no
// source sub-fields at all must still validate clean, since the schema itself imposes no
// concrete sub-field requirement for non-pci types.
func TestValidate_VmNonPCIHostdev_Incomplete_Accepted(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.204.1223
myvm:
  vm:
    source:
      kind: cloud_image
      url: http://x
    libvirt:
      devices:
        hostdevs:
        - type: usb
          source:
            vendor: "0x1234"
`,
	})
	mustValidateOK(t, dir)
}

// TestValidate_VmNoHostdevsAuthored_Accepted proves the overwhelmingly common case — a vm:
// entity with no hostdevs list at all (the corpus-wide norm; GPU passthrough hostdevs are
// auto-detected and injected into the per-host instance.yml overlay at `charly vm create` time,
// never authored directly) — validates clean, with zero extra round-trip cost implied.
func TestValidate_VmNoHostdevsAuthored_Accepted(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.204.1223
myvm:
  vm:
    source:
      kind: cloud_image
      url: http://x
`,
	})
	mustValidateOK(t, dir)
}

// TestValidate_PodKind_NoOpValidateRoundTrip proves the "vm"-only Validates:true scoping: a
// plain pod: node (a substrate kind that does NOT declare Validates) validates clean with no
// deep-check round-trip attempted at all.
func TestValidate_PodKind_NoOpValidateRoundTrip(t *testing.T) {
	dir := writeValidateFixture(t, map[string]string{
		"charly.yml": `version: 2026.204.1223
mypod:
  pod:
    image: x
`,
	})
	mustValidateOK(t, dir)
}
