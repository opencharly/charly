package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

func TestValidateLibvirtSnippet(t *testing.T) {
	tests := []struct {
		name    string
		snippet string
		wantErr bool
	}{
		{"valid channel", `<channel type='unix'><target type='virtio' name='org.qemu.guest_agent.0'/></channel>`, false},
		{"valid hostdev", `<hostdev mode='subsystem' type='pci' managed='yes'><source><address domain='0x0000' bus='0x01' slot='0x00' function='0x0'/></source></hostdev>`, false},
		{"valid cpu", `<cpu mode='host-passthrough'><feature policy='require' name='vmx'/></cpu>`, false},
		{"valid graphics", `<graphics type='spice' autoport='yes'/>`, false},
		{"empty", "", true},
		{"invalid xml", "<broken>", true},
		{"not xml", "hello world", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := vmshared.ValidateLibvirtSnippet(tt.snippet)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateLibvirtSnippet(%q) error = %v, wantErr %v", tt.snippet, err, tt.wantErr)
			}
		})
	}
}

func TestIsDeviceElement(t *testing.T) {
	tests := []struct {
		snippet  string
		isDevice bool
	}{
		{`<channel type='unix'><target type='virtio' name='org.qemu.guest_agent.0'/></channel>`, true},
		{`<disk type='file'><source file='/tmp/test.qcow2'/></disk>`, true},
		{`<graphics type='spice' autoport='yes'/>`, true},
		{`<video><model type='virtio'/></video>`, true},
		{`<hostdev mode='subsystem' type='pci' managed='yes'/>`, true},
		{`<cpu mode='host-passthrough'/>`, false},
		{`<clock offset='utc'/>`, false},
		{`<features><acpi/></features>`, false},
	}
	for _, tt := range tests {
		t.Run(tt.snippet[:20], func(t *testing.T) {
			got := vmshared.IsDeviceElement(tt.snippet)
			if got != tt.isDevice {
				t.Errorf("isDeviceElement(%q) = %v, want %v", tt.snippet, got, tt.isDevice)
			}
		})
	}
}

func TestCollectLibvirtSnippets(t *testing.T) {
	// Post-cutover, CollectLibvirtSnippets only harvests candy-level
	// `libvirt:` fields (box-level `libvirt:` was deleted in the VM
	// hard-cutover; box-level raw snippets now live on the paired
	// kind:vm entity's spec.libvirt.snippets:).
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"test-image": {
				Candy: []string{"layer-a", "layer-b"},
			},
		}),
	}
	layers := map[string]*Candy{
		"layer-a": {
			Name:    "layer-a",
			libvirt: []string{"<channel type='unix'><target type='virtio' name='org.qemu.guest_agent.0'/></channel>"},
		},
		"layer-b": {
			Name: "layer-b",
		},
	}

	snippets := CollectLibvirtSnippets(cfg, layers, "test-image")
	if len(snippets) != 1 {
		t.Fatalf("expected 1 snippet (layer-a only, box-level removed), got %d: %v", len(snippets), snippets)
	}
}

func TestCollectLibvirtSnippets_NonexistentImage(t *testing.T) {
	cfg := &Config{Box: boxMapOf(map[string]spec.BoxConfig{})}
	layers := map[string]*Candy{}
	snippets := CollectLibvirtSnippets(cfg, layers, "nonexistent")
	if snippets != nil {
		t.Fatalf("expected nil, got %v", snippets)
	}
}
