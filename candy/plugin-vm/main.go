// Command plugin-vm is the OUT-OF-TREE charly plugin housing charly's VM-subsystem IMPL. It is
// DUAL-MODE (sdk.Main): it provides TWO capabilities.
//
//   - verb:libvirt — the `charly check libvirt` probe verb plus the internal VM ops
//     (domain-state / list-domains / resolve-spice / resolve-vnc / create / start / stop /
//     destroy / snapshot-internal / qemu-shutdown / domain-xml / list-all-domains) that the
//     spice/vnc/ssh/status/preempt consumers + the vm deploy target RPC. Served OUT-OF-PROCESS
//     over go-plugin gRPC (the host go-builds this binary and dispatches through the registry).
//
//   - command:vm — `charly vm …` (build / create / start / stop / destroy / console / ssh /
//     snapshot / gpu / import / clone / cp-box / list), the externalized VM lifecycle CLI.
//     charly DISPATCHES the command by syscall.Exec'ing this binary in CLI mode (sdk.Main →
//     cliMain, command.go), which RAW-FORWARDS the pass-through args to the hidden in-core
//     `charly __vm <args…>` so the VmCmd Run handlers run in core (they own the loader + the
//     libvirt/qemu backends + the deploy target — none of which this out-of-process plugin can
//     reach), inheriting charly's stdio/TTY for `charly vm console` / `charly vm ssh`.
//
// A standalone Go module (its own go.mod) keeping the go-libvirt + kata-containers/govmm +
// libvirt.org/go/libvirtxml stack OUT of charly's core go.mod. verb:libvirt is served over gRPC
// (the provider registry); command:vm is served via the CLI syscall.Exec path — so command:vm is
// declared in plugin.providers (for the CLI-grammar prescan + baked manifest) but NOT advertised
// in Describe (mirrors candy/plugin-secrets's verb:credential + command:secrets).
package main

import (
	"context"
	"embed"

	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// main is dual-mode (sdk.Main): when charly launches this binary over go-plugin gRPC (the
// handshake cookie is set) it SERVES verb:libvirt; otherwise charly syscall.Exec'd it as a
// command passthrough and it runs the `charly vm …` CLI forwarder (cliMain, command.go) with
// real terminal stdio.
func main() { sdk.Main(&vmProvider{}, &vmMeta{}, cliMain) }

type vmMeta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the ONE gRPC-served capability: the libvirt check verb (nested under
// `charly check` at runtime like kube/adb/appium). The internal VM ops (resolution / lifecycle /
// snapshot / create / qemu-shutdown) ride Invoke via special VmOp words and are NOT Describe
// classes — the hidden `charly __vm` command tree + the display/status/preempt consumers RPC
// them. libvirt keeps its modifiers on charly's core #Op (a schema-less verb, empty InputDef).
// command:vm (`charly vm …`, the externalized CLI) is NOT advertised here: it is dispatched by
// charly syscall.Exec'ing this binary in CLI mode (cliMain), not resolved through the gRPC
// provider registry. The candy's plugin.providers declaration still lists command:vm (the
// CLI-grammar prescan + baked `.providers` manifest).
func (vmMeta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.177.0300",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "libvirt", InputDef: ""},
		},
		schemaFS, "schema")
}

// vmProvider is the out-of-process provider. Its Invoke dispatches the libvirt verb (the in-process
// LibvirtCmd Kong tree) plus the internal VmOp-keyed ops (resolution / lifecycle / snapshot /
// create / qemu-shutdown) that core RPCs.
type vmProvider struct {
	pb.UnimplementedProviderServer
}
