// Command plugin-wl is the OUT-OF-TREE charly plugin serving the `wl`
// live-container check verb (a standalone Go module, its own go.mod). It drives Wayland/sway
// desktop automation inside a running deployment — input (click/type/key/mouse/scroll/drag),
// window management, screenshots, sway IPC, overlays, AT-SPI2, clipboard — by driving the
// venue's compositor tools (wlrctl/grim/wtype/wl-clipboard/swaymsg/kdotool/python3-pyatspi/
// charly-overlay). The host go-builds this binary and serves it OUT-OF-PROCESS over go-plugin
// gRPC via the charly plugin SDK, so the `wl:` verb dispatches through the provider registry
// exactly like a built-in — with the verb keeping its `wl:` discriminator + every modifier
// (x/y/x2/y2/direction/amount/target/text/key/combo/command/action/query/artifact) + the
// #WlMethod enum on charly's core #Op (authoring unchanged: `wl: screenshot`, not
// `plugin: wl`).
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
package main

import (
	"context"
	"embed"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

func main() { sdk.Serve(&provider{}, &meta{}) }

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the plugin's capability (verb:wl) AND its self-contained CUE schema over the
// wire via sdk.BuildCapabilities. wl keeps its entire authoring contract (the #WlMethod enum
// + every modifier) on charly's core #Op — like cdp/vnc/mcp/record/dbus, it has NO
// plugin_input — so the advertised capability carries an EMPTY InputDef and the served schema
// (wl.cue) exists only to satisfy the host's non-empty-schema load gate. The SDK compiles the
// schema standalone here, failing loudly before serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.182.1805",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "wl", InputDef: ""}},
		schemaFS, "schema")
}
