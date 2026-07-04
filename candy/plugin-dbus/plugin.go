// Package dbus is the charly plugin serving the `dbus`
// live-container check verb (an importable root package + its own go.mod). It interacts with
// D-Bus services inside a running deployment — list / call / introspect / notify — driving
// the venue's session bus through gdbus. The host go-builds this binary and serves it
// OUT-OF-PROCESS over go-plugin gRPC via the charly plugin SDK, so the `dbus:` verb
// dispatches through the provider registry exactly like a built-in — with the verb keeping
// its `dbus:` discriminator + every modifier (dest/path/method/args/text/description) on
// charly's core #Op (authoring unchanged: `dbus: list`, not `plugin: dbus`).
//
// EXEC-based external verb (the second, after record): unlike the PORT-based external verbs
// (mcp/spice/kube/cdp/vnc — the host pre-resolves a dial endpoint), dbus drives the venue's
// own session bus. The host attaches its live DeployExecutor over the E3b reverse channel
// (invokeVerbProvider, the executorInvoker branch), and this plugin dials back through the
// SDK (sdk.ExecutorFromInvoke) to run gdbus on the venue (RunCapture). The `dbus` driver
// therefore owns NO podman / SSH machinery and NO godbus — it speaks only gdbus over the
// executor reverse channel.
//
// STRUCTURAL externalization, NOT a dep-shed: godbus stays in charly's core for the Secret
// Service / GPG secrets (enc.go / secret_service.go / secrets_gpg.go). The host-side
// best-effort notification (`charly cmd --notify` / `charly tmux cmd`) keeps working via its
// own in-core gdbus path (notify.go) — also gdbus, never this plugin.
//
// Dual-placement by construction: the SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS
// over go-plugin gRPC when they are not — placement is invisible above the registry.
package dbus

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the dbus provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:dbus + the plugin's self-contained CUE schema (via
// sdk.NewMeta → BuildCapabilities). dbus keeps its entire authoring contract (the
// #DbusMethod enum + every modifier) on charly's core #Op — like cdp/vnc/mcp/record it has
// NO plugin_input — so the capability carries an EMPTY InputDef and the served schema
// (dbus.cue) exists only to satisfy the host's non-empty-schema load gate.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.182.1805",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "dbus", InputDef: ""}},
		schemaFS)
}
