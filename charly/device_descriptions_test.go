package main

import "testing"

// TestDeviceDescriptionsFromEmbedded proves the `charly doctor` device-description map is
// read from the device_descriptions directive in the embedded charly.yml (Phase 4: data
// moved out of the Go var) and matches the canonical set — including the YAML-quoted
// "/dev/dri/renderD*" key (the `*` needs quoting). Fails on any drift / parse breakage.
func TestDeviceDescriptionsFromEmbedded(t *testing.T) {
	want := map[string]string{
		"/dev/dri/renderD*": "GPU render node",
		"/dev/kfd":          "AMD Kernel Fusion Driver (ROCm compute)",
		"/dev/kvm":          "KVM virtualization",
		"/dev/vhost-net":    "vhost network acceleration",
		"/dev/vhost-vsock":  "VM socket communication",
		"/dev/fuse":         "FUSE filesystem",
		"/dev/net/tun":      "TUN/TAP network device",
		"/dev/hwrng":        "hardware random number generator",
	}
	if len(deviceDescriptions) != len(want) {
		t.Fatalf("deviceDescriptions has %d entries, want %d: %v", len(deviceDescriptions), len(want), deviceDescriptions)
	}
	for k, v := range want {
		if deviceDescriptions[k] != v {
			t.Fatalf("deviceDescriptions[%q]=%q, want %q (embedded charly.yml drift)", k, deviceDescriptions[k], v)
		}
	}
}
