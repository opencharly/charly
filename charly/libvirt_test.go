package main

import (
	"testing"

	"github.com/opencharly/sdk/vmshared"
)

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
