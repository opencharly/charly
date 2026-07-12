// Package vm is the charly plugin housing charly's VM-subsystem IMPL. It is a dual-placement plugin —
// the SAME NewProvider()/NewMeta()/CliMain compile INTO charly in-process when listed in
// compiled_plugins (the canonical placement, P10), or cmd/serve serves them OUT-OF-PROCESS when they
// are not. It provides TWO capabilities.
//
//   - verb:libvirt — the `charly check libvirt` probe verb plus the internal VM ops
//     (domain-state / list-domains / resolve-spice / resolve-vnc / create / start / stop /
//     destroy / snapshot-internal / qemu-shutdown / domain-xml / list-all-domains) the
//     spice/vnc/ssh/status/preempt consumers + the vm deploy target dispatch through the registry.
//
//   - command:vm — `charly vm …` (build / create / start / stop / destroy / console / ssh /
//     snapshot / gpu / import / clone / cp-box / list), the VM lifecycle CLI. COMPILED-IN, it
//     dispatches IN-PROC via Invoke(OpRun) (runVmCommand → kong-parse the VmCmd tree — command.go),
//     so the handlers run in charly's OWN process and inherit charly's real stdio/TTY natively
//     (`charly vm console` / `charly vm ssh` stay interactive). They own the CLI + the libvirt/qemu
//     engine (in-package) and reach the host-only Mechanisms over generic seams: the config loader +
//     deploy ledger via HostBuild("config-resolve"/"config-persist"), the VM-disk build engine via
//     HostBuild("vm-build"), egress via verb:egress, preempt via verb:arbiter, GPU via verb:gpu.
//
// A standalone Go module (its own go.mod) carrying the go-libvirt + kata-containers/govmm +
// libvirt.org/go/libvirtxml stack, compiled into charly for the canonical placement. Both
// capabilities are advertised in Describe (NewMeta); command:vm's grammar is prescanned into the CLI.
package vm

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the vm provider (verb:libvirt + the internal VmOp Invoke surface).
func NewProvider() pb.ProviderServer { return &vmProvider{} }

// NewMeta advertises BOTH capabilities via sdk.NewMeta: verb:libvirt (the libvirt check verb, nested
// under `charly check` at runtime like kube/adb/appium) and command:vm (the `charly vm …` CLI). The
// internal VM ops (resolution / lifecycle / snapshot / create / qemu-shutdown) ride Invoke via special
// VmOp words and are NOT Describe classes — the moved VM CLI handlers + the display/status/preempt
// consumers dispatch them in-package / through the registry. The verb's plugin_input validates against
// the served #LibvirtVerbInput (the method enum + every libvirt-exclusive modifier moved here from core
// #Op in the schema-compaction cutover). command:vm is COMPILED-IN and dispatched IN-PROC via
// Invoke(OpRun) (runVmCommand, command.go) — its grammar is prescanned into the CLI from plugin.providers.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.177.0300",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "libvirt", InputDef: "#LibvirtVerbInput", Primary: "method"},
			{Class: "command", Word: "vm"},
		},
		schemaFS)
}

// vmProvider is the out-of-process provider. Its Invoke dispatches the libvirt verb (the in-process
// LibvirtCmd Kong tree) plus the internal VmOp-keyed ops (resolution / lifecycle / snapshot /
// create / qemu-shutdown) that core RPCs.
type vmProvider struct {
	pb.UnimplementedProviderServer
}
