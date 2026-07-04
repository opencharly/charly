package vm

import (
	"strings"
	"testing"

	libvirtxml "libvirt.org/go/libvirtxml"
)

// TestSpiceEndpoint_NoDevice_MatchesSkipSentinel proves the host-side no-SPICE-device SKIP is wired
// correctly: when a resolved VM declares no <graphics type='spice'>, SpiceEndpoint must return an
// error whose text contains noVmDisplayDeviceErr — the SAME substring the host's
// preresolveSpiceEndpoint keys off to return a SKIP (the SPICE-less cachyos-gpu operator) rather
// than a FAIL. Relocated from charly/vm_display_gate_test.go with the VmTarget/SpiceEndpoint
// resolver (the go-libvirt + libvirtxml shed).
func TestSpiceEndpoint_NoDevice_MatchesSkipSentinel(t *testing.T) {
	// A domain with a VNC head but NO spice device.
	parsed := &libvirtxml.Domain{}
	if err := parsed.Unmarshal(`<domain><devices><graphics type='vnc' port='5900'/></devices></domain>`); err != nil {
		t.Fatalf("unmarshal domain XML: %v", err)
	}
	tgt := &VmTarget{XML: parsed, VmName: "cachyos-gpu", DomName: "charly-cachyos-gpu"}
	_, err := tgt.SpiceEndpoint()
	if err == nil {
		t.Fatal("SpiceEndpoint on a spice-less domain: want error, got nil")
	}
	if !strings.Contains(err.Error(), noVmDisplayDeviceErr) {
		t.Errorf("SpiceEndpoint no-device error %q does not contain the skip sentinel %q — "+
			"preresolveSpiceEndpoint would FAIL instead of SKIP", err, noVmDisplayDeviceErr)
	}
}
