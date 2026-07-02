package main

import (
	_ "embed"
	"os/exec"

	"gopkg.in/yaml.v3"
)

// vm_phaseA_shims.go — small host-side impl helpers the out-of-process plugin needs (it runs on the
// host). unmarshalEmbeddedDefaults implements the vmshared.UnmarshalEmbeddedDefaults injection seam
// (charly/vmshared/hooks.go, wired in vmshared_aliases.go). libvirtSessionURI / startLibvirtUserSession
// are a deliberate per-module copy of core's charly/vm.go host-detection helpers (a const + one tiny
// var): the SUBSTANTIAL shared VM code — including qemuSystemBinary + vmDiskDir — now lives ONCE in
// vmshared (vm_helpers.go, aliased in vmshared_aliases.go), and these two are below the bar for
// exporting trivia across the module boundary (R3 — the shared-vs-trivial line). NOT transitional.

// libvirtSessionURI is the rootless per-user libvirt endpoint (extract from vm.go).
const libvirtSessionURI = "qemu:///session"

// startLibvirtUserSession ensures the per-user libvirt daemon is running (extract from vm.go).
var startLibvirtUserSession = func() {
	for _, unit := range []string{"virtqemud.service", "libvirtd.service"} {
		_ = exec.Command("systemctl", "--user", "start", unit).Run()
	}
	if _, err := exec.LookPath("virsh"); err == nil {
		_ = exec.Command("virsh", "-c", "qemu:///session", "list").Run()
	}
}

//go:embed build_defaults.yml
var embeddedCharlyDefaults []byte

// unmarshalEmbeddedDefaults decodes the plugin's embedded build vocab (build_defaults.yml, a copy of
// charly's charly.yml — the ovmf_paths/distro sections the OVMF resolver reads). The out-of-process
// plugin self-resolves OVMF firmware paths from its own embedded vocab since it cannot reach charly's
// //go:embed. Implements the vmshared.UnmarshalEmbeddedDefaults seam (wired in vmshared_aliases.go).
func unmarshalEmbeddedDefaults(dst any) {
	_ = yaml.Unmarshal(embeddedCharlyDefaults, dst)
}
