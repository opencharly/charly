// Package record is the charly plugin serving the `record`
// live-container check verb (an importable root package + its own go.mod). It manages
// recording sessions — terminal (asciinema) or desktop video (pixelflux/wf-recorder) —
// inside a running deployment: list / start / stop / cmd. The host go-builds this binary
// and serves it OUT-OF-PROCESS over go-plugin gRPC via the charly plugin SDK, so the
// `record:` verb dispatches through the provider registry exactly like a built-in. Since
// the schema-compaction cutover an authored `record:` step desugars to the internal
// plugin/plugin_input envelope, and every record-exclusive modifier
// (method/record_name/record_mode/record_fps/record_audio/text/artifact) lives in the
// plugin's OWN #RecordInput (schema/record.cue → the generated params.RecordInput).
//
// FIRST consumer of the executor reverse channel: unlike the PORT-based external verbs
// (mcp/spice/kube — the host pre-resolves a dial endpoint), record is EXEC-based. The host
// attaches its live DeployExecutor over the E3b reverse channel (invokeVerbProvider, the
// executorInvoker branch), and this plugin dials back through the SDK
// (sdk.ExecutorFromInvoke) to drive the venue: RunCapture runs the asciinema/wf-recorder
// commands in-container via tmux, and GetFile pulls the produced .cast/.mp4 artifact back
// to the host. The `record` driver therefore owns NO podman / SSH machinery — it speaks
// only the executor reverse channel.
//
// Dual-placement by construction: the SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS
// over go-plugin gRPC when they are not — placement is invisible above the registry.
package record

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the record provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:record + the plugin's self-contained CUE schema (via
// sdk.NewMeta → BuildCapabilities). The verb's entire authoring contract — the
// method enum + every record-exclusive modifier — lives in the served #RecordInput
// (schema/record.cue), which the host splices onto the base and validates every
// authored `record:` step's plugin_input against.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.182.1805",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "record", InputDef: "#RecordInput"}},
		schemaFS)
}
