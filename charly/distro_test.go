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

