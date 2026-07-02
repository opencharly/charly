package main

import "testing"

// TestInstallHintsFromEmbedded proves the host-dependency install-hint map is read from the
// install_hints directive in the embedded charly.yml (Phase 4: data moved out of the Go
// var installHints) and matches the canonical set — including the colon-bearing "AUR:"
// values that MUST be YAML-quoted. Fails on any drift / parse breakage.
func TestInstallHintsFromEmbedded(t *testing.T) {
	if len(installHints) != 19 {
		t.Fatalf("installHints has %d binaries, want 19", len(installHints))
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
		if got := installHints[c.bin][c.distro]; got != c.want {
			t.Fatalf("installHints[%q][%q]=%q, want %q (embedded charly.yml drift / YAML-quoting bug)", c.bin, c.distro, got, c.want)
		}
	}
}
