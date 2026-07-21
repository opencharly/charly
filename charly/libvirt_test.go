package main

import (
	"testing"

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
