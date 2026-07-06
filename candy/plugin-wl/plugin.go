// Package wl is the charly plugin serving the `wl`
// live-container check verb (an importable root package + its own go.mod). It drives Wayland/sway
// desktop automation inside a running deployment — input (click/type/key/mouse/scroll/drag),
// window management, screenshots, sway IPC, overlays, AT-SPI2, clipboard — by driving the
// venue's compositor tools (wlrctl/grim/wtype/wl-clipboard/swaymsg/kdotool/python3-pyatspi/
// charly-overlay). The host go-builds this binary and serves it OUT-OF-PROCESS over go-plugin
// gRPC via the charly plugin SDK, so the `wl:` verb dispatches through the provider registry
// exactly like a built-in. The wl-exclusive fields (the verb method enum +
// x/y/x2/y2/button/direction/amount/target/text/key/combo/command/action/query + the
// artifact validators) live in the plugin's OWN #WlInput (schema/wl.cue), decoded from the
// step's desugared plugin_input; only the genuinely shared step matchers
// (exit_status/stdout/stderr) still ride charly's core #Op.
//
// EXEC-based external verb (the third, after record + dbus): unlike the PORT-based external
// verbs (mcp/spice/kube/cdp/vnc — the host pre-resolves a dial endpoint), wl drives the
// venue's own compositor. The host attaches its live DeployExecutor over the E3b reverse
// channel (invokeVerbProvider, the executorInvoker branch), and this plugin dials back
// through the SDK (sdk.ExecutorFromInvoke) to RunCapture the venue's wl tools (screenshot
// pulls the PNG via GetFile). The `wl` driver therefore owns NO podman / SSH machinery and NO
// CDP client — the CLI-only `--from-cdp`/`--from-sway`/`--from-x11` coordinate translation
// was DROPPED (the declarative `wl: click` uses X/Y directly), exactly as cdp/vnc dropped
// their From* flags. wl is the LAST live-container verb to leave charly's core: after it,
// ZERO check verbs are compiled-in.
//
// Dual-placement by construction: the SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS
// over go-plugin gRPC when they are not — placement is invisible above the registry.
package wl

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the wl provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:wl (plugin_input #WlInput) + the plugin's self-contained CUE
// schema (via sdk.NewMeta → BuildCapabilities). The host splices the served schema onto
// its base and validates every authored `wl` step's plugin_input against #WlInput (the
// verb method enum + the input/window/artifact modifiers — the wl-exclusive fields that
// left core #Op in the schema-compaction cutover).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.187.1200",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "wl", InputDef: "#WlInput"}},
		schemaFS)
}
