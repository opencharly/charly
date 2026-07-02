package main

import "testing"

// TestVmCreate_HostEgressValidatesReturnedDomainXML proves FU-1: the two-phase
// ValidateOnly create (runVmSpecCreate) has the HOST decode the plugin's rendered
// libvirt domain XML (vmCreateRenderedXML) and egress-gate it (ValidateXMLEgress)
// BEFORE authorizing create. Before this fix the out-of-process plugin no-op'd egress,
// so a policy-violating VM domain would be created unchecked. This test exercises the
// host-side decode + gate that the create path runs between its two RPC passes.
func TestVmCreate_HostEgressValidatesReturnedDomainXML(t *testing.T) {
	// A validate-pass reply carrying a policy-violating domain XML (empty <name> — the
	// same case the egress schema rejects in TestValidateXMLEgress_LibvirtDomain).
	badReply := []byte(`{"ok":true,"rendered_domain_xml":"<domain type='kvm'>\n  <name></name>\n  <memory unit='KiB'>8388608</memory>\n</domain>\n"}`)
	xml := vmCreateRenderedXML(badReply)
	if xml == "" {
		t.Fatal("vmCreateRenderedXML: expected the plugin's rendered domain XML, got empty")
	}
	if err := ValidateXMLEgress("libvirt_domain_xml", "vm:test", xml); err == nil {
		t.Fatal("host egress gate must REJECT the policy-violating domain XML the plugin returned")
	}

	// A valid domain XML passes the gate (the happy path the check-fedora-vm bed exercises).
	goodReply := []byte(`{"ok":true,"rendered_domain_xml":"<domain type='kvm'>\n  <name>vm1</name>\n  <memory unit='KiB'>8388608</memory>\n  <os><type>hvm</type></os>\n</domain>\n"}`)
	if err := ValidateXMLEgress("libvirt_domain_xml", "vm:test", vmCreateRenderedXML(goodReply)); err != nil {
		t.Fatalf("host egress gate must ACCEPT a valid domain XML: %v", err)
	}

	// The QEMU backend returns no domain XML — the gate is skipped (its cloud-init is
	// already egress-validated host-side when the host builds the seed ISO).
	if x := vmCreateRenderedXML([]byte(`{"ok":true}`)); x != "" {
		t.Fatalf("vmCreateRenderedXML: expected empty for a no-XML (QEMU) reply, got %q", x)
	}
}
