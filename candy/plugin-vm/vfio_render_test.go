package main

import (
	"strings"
	"testing"
)

// TestRenderHostdevAndFeaturesXML verifies the VFIO hostdev + libvirt-features domain-XML rendering.
// Relocated from charly/vfio_test.go alongside the RenderDomainXML renderer during the
// go-libvirt + libvirtxml shed (the renderer is now the out-of-process candy/plugin-vm's).
func TestRenderHostdevAndFeaturesXML(t *testing.T) {
	romFile := "off"
	spec := &VmSpec{
		Source:   VmSource{Kind: "cloud_image", URL: "http://x/y.qcow2"},
		Firmware: "uefi-insecure",
		Libvirt: &LibvirtDomain{
			Features: &LibvirtFeatures{
				KVM:    &LibvirtKVM{Hidden: "on"},
				HyperV: &LibvirtHyperV{VendorID: &LibvirtVendorID{State: "on", Value: "ovgpu123456"}},
			},
			Devices: &LibvirtDevices{
				Hostdevs: []LibvirtHostdev{{
					Type:    "pci",
					Managed: "yes",
					Source:  map[string]string{"domain": "0x0000", "bus": "0x01", "slot": "0x00", "function": "0x0"},
					ROM:     map[string]string{"bar": romFile},
					Driver:  map[string]string{"name": "vfio"},
				}},
			},
		},
	}
	rt := VmRuntimeParams{Name: "charly-test", RamMB: 2048, Cpus: 2, HostArch: "x86_64"}
	xmlOut, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("RenderDomainXML: %v", err)
	}
	for _, want := range []string{
		"<hostdev", `managed="yes"`, `type="pci"`,
		`domain="0x0000"`, `bus="0x01"`, `slot="0x00"`, `function="0x0"`,
		`<rom bar="off"`, `<driver name="vfio"`,
		"<kvm>", `<hidden state="on"`, `<vendor_id state="on" value="ovgpu123456"`,
	} {
		if !strings.Contains(xmlOut, want) {
			t.Errorf("domain XML missing %q\n---\n%s", want, xmlOut)
		}
	}
}
