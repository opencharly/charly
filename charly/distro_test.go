package main

import (
	"testing"
)

func TestParseOsReleaseArch(t *testing.T) {
	content := `NAME="Arch Linux"
PRETTY_NAME="Arch Linux"
ID=arch
BUILD_ID=rolling`
	d := parseOsRelease(content)
	if d.ID != "arch" {
		t.Errorf("ID = %q, want %q", d.ID, "arch")
	}
	if d.Name != "Arch Linux" {
		t.Errorf("Name = %q, want %q", d.Name, "Arch Linux")
	}
	if d.Manager != "pacman -S" {
		t.Errorf("Manager = %q, want %q", d.Manager, "pacman -S")
	}
}

func TestParseOsReleaseFedora(t *testing.T) {
	content := `NAME="Fedora Linux"
ID=fedora
VERSION_ID=43`
	d := parseOsRelease(content)
	if d.ID != "fedora" {
		t.Errorf("ID = %q, want %q", d.ID, "fedora")
	}
	if d.Manager != "sudo dnf install" {
		t.Errorf("Manager = %q, want %q", d.Manager, "sudo dnf install")
	}
}

func TestParseOsReleaseDebian(t *testing.T) {
	content := `NAME="Debian GNU/Linux"
ID=debian
VERSION_ID="12"`
	d := parseOsRelease(content)
	if d.ID != "debian" {
		t.Errorf("ID = %q, want %q", d.ID, "debian")
	}
	if d.Manager != "sudo apt-get install" {
		t.Errorf("Manager = %q, want %q", d.Manager, "sudo apt-get install")
	}
}

func TestParseOsReleaseUbuntu(t *testing.T) {
	content := `NAME="Ubuntu"
ID=ubuntu
ID_LIKE=debian`
	d := parseOsRelease(content)
	if d.ID != "ubuntu" {
		t.Errorf("ID = %q, want %q", d.ID, "ubuntu")
	}
	if d.Manager != "sudo apt-get install" {
		t.Errorf("Manager = %q, want %q", d.Manager, "sudo apt-get install")
	}
}

func TestParseOsReleaseUnknown(t *testing.T) {
	content := `NAME="NixOS"
ID=nixos`
	d := parseOsRelease(content)
	if d.ID != "nixos" {
		t.Errorf("ID = %q, want %q", d.ID, "nixos")
	}
	if d.Manager != "" {
		t.Errorf("Manager = %q, want empty", d.Manager)
	}
}

func TestInstallHintArch(t *testing.T) {
	d := Distro{ID: "arch", Manager: "pacman -S"}
	tests := []struct {
		binary string
		want   string
	}{
		{"docker", "pacman -S docker"},
		{"qemu-system-x86_64", "pacman -S qemu-full"},
		{"virsh", "pacman -S libvirt"},
		{"gocryptfs", "pacman -S gocryptfs"},
		{"gvproxy", "yay -S gvisor-tap-vsock"},
		{"cloudflared", "yay -S cloudflared-bin"},
	}
	for _, tt := range tests {
		got := d.installHint(tt.binary)
		if got != tt.want {
			t.Errorf("installHint(%q) = %q, want %q", tt.binary, got, tt.want)
		}
	}
}

func TestInstallHintFedora(t *testing.T) {
	d := Distro{ID: "fedora", Manager: "sudo dnf install"}
	got := d.installHint("qemu-system-x86_64")
	want := "sudo dnf install qemu-kvm"
	if got != want {
		t.Errorf("installHint(qemu-system-x86_64) = %q, want %q", got, want)
	}
}

func TestInstallHintUbuntuFallsBackToDebian(t *testing.T) {
	d := Distro{ID: "ubuntu", Manager: "sudo apt-get install"}
	got := d.installHint("virsh")
	want := "sudo apt-get install libvirt-clients"
	if got != want {
		t.Errorf("installHint(virsh) = %q, want %q", got, want)
	}
}

func TestInstallHintUnknownBinary(t *testing.T) {
	d := Distro{ID: "arch", Manager: "pacman -S"}
	got := d.installHint("some-unknown-tool")
	want := "pacman -S some-unknown-tool"
	if got != want {
		t.Errorf("installHint(some-unknown-tool) = %q, want %q", got, want)
	}
}

func TestInstallHintNoManager(t *testing.T) {
	d := Distro{ID: "nixos", Manager: ""}
	got := d.installHint("docker")
	if got != "docker" {
		t.Errorf("installHint(docker) = %q, want %q", got, "docker")
	}
}
