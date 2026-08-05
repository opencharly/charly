package doctor

import "testing"

// data_test.go — ported from charly/device_descriptions_test.go + charly/install_hints_test.go
// (K5 seam-death: these two embedded data tables moved into this plugin's own embed, data.yml).

// TestDeviceDescriptionsFromEmbedded proves the device-description map is read from the
// device_descriptions directive in this plugin's embedded data.yml and matches the canonical
// set — including the YAML-quoted "/dev/dri/renderD*" key (the `*` needs quoting). Fails on
// any drift / parse breakage.
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
	got := doctorData.DeviceDescriptions
	if len(got) != len(want) {
		t.Fatalf("DeviceDescriptions has %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("DeviceDescriptions[%q]=%q, want %q (embedded data.yml drift)", k, got[k], v)
		}
	}
}

// TestInstallHintsFromEmbedded proves the host-dependency install-hint map is read from the
// install_hints directive in this plugin's embedded data.yml and matches the canonical set —
// including the colon-bearing "AUR:" values that MUST be YAML-quoted. Fails on any drift /
// parse breakage.
func TestInstallHintsFromEmbedded(t *testing.T) {
	got := doctorData.InstallHints
	if len(got) != 19 {
		t.Fatalf("InstallHints has %d binaries, want 19", len(got))
	}
	cases := []struct{ bin, distro, want string }{
		{"docker", "fedora", "docker-ce"},
		{"podman", "debian", "podman"},
		{"qemu-system-x86_64", "debian", "qemu-system-x86"},
		{"qemu-system-aarch64", "debian", "qemu-system-arm"},
		{"virsh", "fedora", "libvirt-client"},
		{"script", "debian", "bsdutils"},
		{"cloudflared", "arch", "AUR: yay -S cloudflared-bin"},
		{"gvproxy", "arch", "AUR: yay -S gvisor-tap-vsock"},
		{"gvproxy", "debian", "golang-github-containers-gvisor-tap-vsock"},
	}
	for _, c := range cases {
		if val := got[c.bin][c.distro]; val != c.want {
			t.Fatalf("InstallHints[%q][%q]=%q, want %q (embedded data.yml drift / YAML-quoting bug)", c.bin, c.distro, val, c.want)
		}
	}
}
