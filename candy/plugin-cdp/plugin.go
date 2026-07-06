// Package cdp is the charly plugin serving the `cdp` Chrome-DevTools-Protocol
// check verb (an importable root package + its own go.mod). It probes a live
// deployment's Chrome over CDP — open/list/close/text/html/url/eval/axtree/coords/
// raw/wait/screenshot/click/type plus the SPA remote-desktop input group —
// speaking the DevTools HTTP (/json) + per-tab CDP WebSocket surface via
// golang.org/x/net/websocket. Since the schema-compaction cutover an authored
// `cdp:` step desugars to the internal plugin/plugin_input envelope, and every
// cdp-exclusive modifier (method/tab/url/expression/selector/…) lives in the
// plugin's OWN #CdpInput (schema/cdp.cue → the generated params.CdpInput).
// Dual-placement by construction: the SAME NewProvider()/NewMeta()
// compile INTO charly in-process when listed in compiled_plugins, or cmd/serve
// serves them OUT-OF-PROCESS over go-plugin gRPC when they are not — placement is
// invisible above the registry.
//
// The plugin owns NO podman / venue / port-mapping machinery — it resolves the deployment's
// CDP port 9222 to a host-reachable DevTools base URL via the generic cc.ResolveEndpoint
// reverse-leg (the host owns that machinery), so this module dials a plain URL and needs no
// container inspection at all.
package cdp

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the cdp verb provider (the Invoke dispatch surface).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:cdp + the plugin's self-contained CUE schema (via
// sdk.NewMeta → BuildCapabilities). The verb's entire authoring contract — the
// method enum + every cdp-exclusive modifier — lives in the served #CdpInput
// (schema/cdp.cue), which the host splices onto the base and validates every
// authored `cdp:` step's plugin_input against.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.178.0900",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "cdp", InputDef: "#CdpInput"}},
		schemaFS)
}
