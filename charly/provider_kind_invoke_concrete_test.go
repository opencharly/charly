package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// vmGenericNode parses a single top-level `<name>: {vm: {...}}` document through the REAL
// production loader (genericNodesFromDoc) and returns the "vm"-discriminated genericNode — the
// exact shape validateKindValueCUE receives in production (via foldSubstrateKind).
func vmGenericNode(t *testing.T, doc string) *genericNode {
	t.Helper()
	var ydoc yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &ydoc); err != nil {
		t.Fatalf("parse fixture yaml: %v", err)
	}
	nodes, err := genericNodesFromDoc(&ydoc)
	if err != nil {
		t.Fatalf("genericNodesFromDoc: %v", err)
	}
	for _, gn := range nodes {
		if gn.disc == "vm" {
			return gn
		}
	}
	t.Fatalf("no vm-discriminated node found in fixture: %s", doc)
	return nil
}

// TestValidateKindValueConcrete_PCIHostdevMissingSlotFunction_Rejected is the RDD-proven-live
// regression test for the validation-correctness batch: a `vm:` libvirt PCI hostdev whose
// slot/function were never authored MUST now fail validateKindValueCUE (the real production
// load-time gate, reached from `charly box validate`/LoadUnified via foldSubstrateKind) — it
// silently passed (exit 0) before this fix.
func TestValidateKindValueConcrete_PCIHostdevMissingSlotFunction_Rejected(t *testing.T) {
	gn := vmGenericNode(t, `myvm:
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
`)
	err := validateKindValueCUE(gn)
	if err == nil {
		t.Fatal("expected validateKindValueCUE to REJECT a PCI hostdev missing slot/function, but it passed")
	}
	t.Logf("got expected rejection: %v", err)
}

// TestValidateKindValueConcrete_PCIHostdevComplete_Accepted proves the fix is narrowly scoped:
// a FULLY-specified PCI hostdev still passes.
func TestValidateKindValueConcrete_PCIHostdevComplete_Accepted(t *testing.T) {
	gn := vmGenericNode(t, `myvm:
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
`)
	if err := validateKindValueCUE(gn); err != nil {
		t.Fatalf("expected a fully-specified PCI hostdev to be ACCEPTED, got: %v", err)
	}
}

// TestValidateKindValueConcrete_NonPCIHostdevIncomplete_Accepted proves the check is scoped to
// PCI hostdevs only (per the schema's own `if type == "pci"` conditional requirement) — a usb
// hostdev with no source sub-fields at all must still pass, since the schema itself imposes no
// concrete sub-field requirement for non-pci types.
func TestValidateKindValueConcrete_NonPCIHostdevIncomplete_Accepted(t *testing.T) {
	gn := vmGenericNode(t, `myvm:
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
`)
	if err := validateKindValueCUE(gn); err != nil {
		t.Fatalf("expected a usb hostdev with no PCI-shaped source to be ACCEPTED, got: %v", err)
	}
}

// TestValidateKindValueConcrete_NoHostdevsAuthored_Accepted proves the overwhelmingly common
// case — a vm: entity with no hostdevs list at all (the corpus-wide norm; GPU passthrough
// hostdevs are auto-detected and injected into the per-host instance.yml overlay at `charly vm
// create` time, never authored directly) — takes the fast no-op path and passes.
func TestValidateKindValueConcrete_NoHostdevsAuthored_Accepted(t *testing.T) {
	gn := vmGenericNode(t, `myvm:
  vm:
    source:
      kind: cloud_image
      url: http://x
`)
	if err := validateKindValueCUE(gn); err != nil {
		t.Fatalf("expected a vm with no hostdevs to be ACCEPTED, got: %v", err)
	}
}

// TestValidateKindValueConcrete_NonVMKind_NoOp proves the check is a no-op for every other
// value-gated kind (pod/k8s/local/android/candy) — it inspects only "vm".
func TestValidateKindValueConcrete_NonVMKind_NoOp(t *testing.T) {
	var ydoc yaml.Node
	doc := `mypod:
  pod:
    image: x
`
	if err := yaml.Unmarshal([]byte(doc), &ydoc); err != nil {
		t.Fatalf("parse fixture yaml: %v", err)
	}
	nodes, err := genericNodesFromDoc(&ydoc)
	if err != nil {
		t.Fatalf("genericNodesFromDoc: %v", err)
	}
	found := false
	for _, gn := range nodes {
		if gn.disc != "pod" {
			continue
		}
		found = true
		if err := validateKindValueCUE(gn); err != nil {
			t.Fatalf("expected a plain pod: node to be ACCEPTED (concreteness check is vm-only), got: %v", err)
		}
	}
	if !found {
		t.Fatal("no pod-discriminated node found in fixture")
	}
}
