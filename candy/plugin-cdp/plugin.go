// Package cdp is the charly plugin serving the `cdp` Chrome-DevTools-Protocol
// check verb (an importable root package + its own go.mod). It probes a live
// deployment's Chrome over CDP — open/list/close/text/html/url/eval/axtree/coords/
// raw/wait/screenshot/click/type plus the SPA remote-desktop input group —
// speaking the DevTools HTTP (/json) + per-tab CDP WebSocket surface via
// golang.org/x/net/websocket. The verb keeps its `cdp:` discriminator + every
// modifier (tab/url/expression/selector/…) on charly's core #Op (authoring
// unchanged). Dual-placement by construction: the SAME NewProvider()/NewMeta()
// compile INTO charly in-process when listed in compiled_plugins, or cmd/serve
// serves them OUT-OF-PROCESS over go-plugin gRPC when they are not — placement is
// invisible above the registry.
//
// The plugin owns NO podman / venue / port-mapping machinery — the host pre-resolves the
// deployment's CDP port 9222 to a host-reachable DevTools base URL (preresolveCdpEndpoint,
// charly/cdp_preresolve.go) and hands it over via the check env, so this module dials a
// plain URL and needs no container inspection at all.
package cdp

import (
	"context"
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the cdp verb provider (the Invoke dispatch surface).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the plugin's capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the plugin's capability (verb:cdp) AND its self-contained CUE schema
// over the wire via sdk.BuildCapabilities. cdp keeps its entire authoring contract (the
// #CdpMethod enum + every modifier) on charly's core #Op — like mcp/vnc/spice, it has NO
// plugin_input — so the advertised capability carries an EMPTY InputDef and the served
// schema (cdp.cue) exists only to satisfy the host's non-empty-schema load gate. The SDK
// compiles the schema standalone here, failing loudly before serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.178.0900",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "cdp", InputDef: ""}},
		schemaFS, "schema")
}
