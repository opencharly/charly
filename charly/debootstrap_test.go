package main

import (
	"testing"

	"github.com/opencharly/sdk/buildkit"
)

// TestDebootstrapDef_YamlParse verifies the new fields added to DebootstrapDef
// (Components, IncludePackages, BasePackages, ExtraRepos) deserialize from
// the YAML shape used in build.yml / charly.yml.
func TestDebootstrapDef_YamlParse(t *testing.T) {
	yamlText := `
distro:
  debian:
    bootstrap:
      install_cmd: "apt-get install -y"
      package: []
    debootstrap:
      suite: trixie
      mirror: http://deb.debian.org/debian
      variant: minbase
      components: "main"
      include_package:
        - ca-certificates
        - gnupg
      base_package:
        - linux-image-amd64
        - grub-efi-amd64
        - openssh-server
      extra_repo:
        - name: debian-security
          url: http://security.debian.org/debian-security
          suite: trixie-security
          components: "main"
`
	var dc buildkit.DistroConfig
	if err := decodeViaCUEForTest(t, yamlText, &dc); err != nil {
		t.Fatalf("unmarshaling debootstrap distro: %v", err)
	}
	def, ok := dc.Distro["debian"]
	if !ok {
		t.Fatal("expected debian distro")
	}
	d := def.Debootstrap
	if d == nil {
		t.Fatal("expected debian.debootstrap to be populated")
	}
	if d.Suite != "trixie" {
		t.Errorf("Suite = %q, want trixie", d.Suite)
	}
	if d.Mirror != "http://deb.debian.org/debian" {
		t.Errorf("Mirror = %q", d.Mirror)
	}
	if d.Variant != "minbase" {
		t.Errorf("Variant = %q, want minbase", d.Variant)
	}
	if d.Components != "main" {
		t.Errorf("Components = %q, want main", d.Components)
	}
	if len(d.IncludePackages) != 2 || d.IncludePackages[0] != "ca-certificates" {
		t.Errorf("IncludePackages = %v", d.IncludePackages)
	}
	if len(d.BasePackages) != 3 || d.BasePackages[1] != "grub-efi-amd64" {
		t.Errorf("BasePackages = %v", d.BasePackages)
	}
	if len(d.ExtraRepos) != 1 || d.ExtraRepos[0].Name != "debian-security" {
		t.Errorf("ExtraRepos = %+v", d.ExtraRepos)
	}
	if d.ExtraRepos[0].URL != "http://security.debian.org/debian-security" {
		t.Errorf("ExtraRepos[0].URL = %q", d.ExtraRepos[0].URL)
	}
}

// TestDebootstrapDef_UbuntuInheritsDebian verifies that ubuntu (which sets
// inherits: debian) gets its OWN debootstrap block — the per-field merge in
// ResolveInherits prefers the child's non-nil sub-block.
func TestDebootstrapDef_UbuntuInheritsDebian(t *testing.T) {
	yamlText := `
distro:
  debian:
    bootstrap:
      install_cmd: "apt-get install -y"
      package: []
    debootstrap:
      suite: trixie
      mirror: http://deb.debian.org/debian
      base_package: [linux-image-amd64]
    bootloader:
      install_template: "BOOTLOADER-DEBIAN"
  ubuntu:
    inherits: debian
    bootstrap:
      install_cmd: ""
      package: []
    debootstrap:
      suite: noble
      mirror: http://archive.ubuntu.com/ubuntu
      components: "main universe"
      base_package: [linux-image-generic]
`
	var dc buildkit.DistroConfig
	if err := decodeViaCUEForTest(t, yamlText, &dc); err != nil {
		t.Fatalf("unmarshaling: %v", err)
	}
	resolved := dc.ResolveDistro([]string{"ubuntu"})
	if resolved == nil {
		t.Fatal("ResolveDistro(ubuntu) returned nil")
	}
	if resolved.Debootstrap == nil {
		t.Fatal("ubuntu.debootstrap nil after inherit-resolve")
	}
	if resolved.Debootstrap.Suite != "noble" {
		t.Errorf("Suite = %q, want noble (child wins)", resolved.Debootstrap.Suite)
	}
	if resolved.Debootstrap.Mirror != "http://archive.ubuntu.com/ubuntu" {
		t.Errorf("Mirror = %q (child should win)", resolved.Debootstrap.Mirror)
	}
	if len(resolved.Debootstrap.BasePackages) != 1 || resolved.Debootstrap.BasePackages[0] != "linux-image-generic" {
		t.Errorf("BasePackages = %v, want [linux-image-generic]", resolved.Debootstrap.BasePackages)
	}
	// Bootloader inherited from debian parent — child has no bootloader: block.
	if resolved.Bootloader == nil || resolved.Bootloader.InstallTemplate != "BOOTLOADER-DEBIAN" {
		t.Errorf("ubuntu should inherit bootloader from debian; got %+v", resolved.Bootloader)
	}
}

// The TestBaseBootstrapPackages_* suite (DebootstrapDispatch / PacstrapStillWorks /
// NilDistro) moved to candy/plugin-vm/vm_bootstrap_engine_test.go with baseBootstrapPackages
// itself (P8b-rest: the VM-bootstrap disk-build engine moved into the plugin). The sibling
// bootstrapPackagesForBox in charly/build.go (the box-build `from: builder:` path) is
// unaffected by this move — untouched, still core, still (pre-existing, unrelated to this
// cutover) without its own dedicated unit test.
