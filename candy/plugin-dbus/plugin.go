// Package dbus is the charly plugin serving the `dbus`
// live-container check verb (an importable root package + its own go.mod). It interacts with
// D-Bus services inside a running deployment — list / call / introspect / notify — driving
// the venue's session bus through gdbus. The host go-builds this binary and serves it
// OUT-OF-PROCESS over go-plugin gRPC via the charly plugin SDK, so the `dbus:` verb
// dispatches through the provider registry exactly like a built-in. The dbus-exclusive
// fields (the verb method enum + dest/path/member/arg/text) live in the plugin's OWN
// #DbusInput (schema/dbus.cue), decoded from the step's desugared plugin_input; only the
// genuinely shared step modifiers (exit_status/stdout/stderr matchers, description) still
// ride charly's core #Op.
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
// best-effort notification (`charly cmd --notify`) keeps working via its
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

// NewMeta advertises verb:dbus (plugin_input #DbusInput) + the plugin's self-contained
// CUE schema (via sdk.NewMeta → BuildCapabilities). The host splices the served schema
// onto its base and validates every authored `dbus` step's plugin_input against
// #DbusInput (the verb method enum + dest/path/member/arg/text — the dbus-exclusive
// fields that left core #Op in the schema-compaction cutover).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.187.1200",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "dbus", InputDef: "#DbusInput"}},
		schemaFS)
}
