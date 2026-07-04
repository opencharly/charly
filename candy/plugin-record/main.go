// Command plugin-record is the OUT-OF-TREE charly plugin serving the `record`
// live-container check verb (a standalone Go module, its own go.mod). It manages
// recording sessions — terminal (asciinema) or desktop video (pixelflux/wf-recorder) —
// inside a running deployment: list / start / stop / cmd. The host go-builds this binary
// and serves it OUT-OF-PROCESS over go-plugin gRPC via the charly plugin SDK, so the
// `record:` verb dispatches through the provider registry exactly like a built-in — with
// the verb keeping its `record:` discriminator + every modifier
// (record_name/record_mode/record_fps/record_audio) on charly's core #Op (authoring
// unchanged: `record: start`, not `plugin: record`).
//
// FIRST consumer of the executor reverse channel: unlike the PORT-based external verbs
// (mcp/spice/kube — the host pre-resolves a dial endpoint), record is EXEC-based. The host
// attaches its live DeployExecutor over the E3b reverse channel (invokeVerbProvider, the
// executorInvoker branch), and this plugin dials back through the SDK
// (sdk.ExecutorFromInvoke) to drive the venue: RunCapture runs the asciinema/wf-recorder
// commands in-container via tmux, and GetFile pulls the produced .cast/.mp4 artifact back
// to the host. The `record` driver therefore owns NO podman / SSH machinery — it speaks
// only the executor reverse channel.
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

// Describe ships the plugin's capability (verb:record) AND its self-contained CUE
// schema over the wire via sdk.BuildCapabilities. record keeps its entire authoring
// contract (the #RecordMethod enum + every modifier) on charly's core #Op — like
// cdp/vnc/mcp/spice, it has NO plugin_input — so the advertised capability carries an
// EMPTY InputDef and the served schema (record.cue) exists only to satisfy the host's
// non-empty-schema load gate. The SDK compiles the schema standalone here, failing
// loudly before serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.182.1805",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "record", InputDef: ""}},
		schemaFS, "schema")
}
