package vm

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

// vm_phaseA_shims.go — small host-side impl helpers the out-of-process plugin needs (it runs on the
// host). unmarshalEmbeddedDefaults implements the vmshared.UnmarshalEmbeddedDefaults injection seam
// (sdk/vmshared/hooks.go, wired in this candy's own vmshared_aliases.go init()). libvirtSessionURI is
// a deliberate per-module copy of core's charly/vm.go host-detection const: the SUBSTANTIAL shared VM
// code — including qemuSystemBinary + vmshared.VmDiskDir — now lives ONCE in vmshared (vm_helpers.go;
// the ones this module actually calls are aliased in this candy's own vmshared_aliases.go), and this
// one is below the bar for exporting trivia across the module boundary (R3 — the shared-vs-trivial
// line). startLibvirtUserSession, its former sibling here, WAS this same kind of trivial per-module
// copy — but it turned out to be a genuine R3 duplicate (byte-identical in core AND here) rather than
// a trivial one, so it R3-hoisted to sdk/vmshared.StartLibvirtUserSession instead (F6 vm-lifecycle
// move, coneB-vmlifecycle) — this file's old "NOT transitional" framing for that half was the call
// that changed, not libvirtSessionURI's.

// libvirtSessionURI is the rootless per-user libvirt endpoint (extract from vm.go).
const libvirtSessionURI = "qemu:///session"

//go:embed build_defaults.yml
var embeddedCharlyDefaults []byte

// unmarshalEmbeddedDefaults decodes the plugin's embedded build vocab (build_defaults.yml, a copy of
// charly's charly.yml — the ovmf_paths/distro sections the OVMF resolver reads). The out-of-process
// plugin self-resolves OVMF firmware paths from its own embedded vocab since it cannot reach charly's
// //go:embed. Implements the vmshared.UnmarshalEmbeddedDefaults seam (wired in this candy's own vmshared_aliases.go init()).
func unmarshalEmbeddedDefaults(dst any) {
	_ = yaml.Unmarshal(embeddedCharlyDefaults, dst)
}
