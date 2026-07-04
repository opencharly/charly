// Package mcp is the charly plugin serving TWO MCP capabilities: the `mcp` MCP-protocol check
// VERB and the `charly mcp` COMMAND. It is an importable dual-placement plugin — the SAME
// NewProvider()/NewMeta()/CliMain compile INTO charly in-process when listed in compiled_plugins,
// or cmd/serve serves them OUT-OF-PROCESS when they are not. Both speak the Model Context Protocol via
// github.com/modelcontextprotocol/go-sdk, which lives HERE — out of charly's core (the C1
// dep-shed removed the go-sdk family from the core binary entirely; charly's core imports
// no go-sdk).
//
//   - verb:mcp — the MCP check verb: probes MCP servers declared via mcp_provides on a live
//     deployment (ping, servers, list-tools/resources/prompts, call, read). The host
//     go-builds this binary and serves it OUT-OF-PROCESS over go-plugin gRPC via the charly
//     plugin SDK, so the `mcp:` verb dispatches through the provider registry exactly like a
//     built-in — keeping its `mcp:` discriminator + every modifier (mcp_name/tool/uri/input)
//     on charly's core #Op (authoring unchanged). The fifth external dep-shed (after
//     candy/plugin-appium, candy/plugin-adb, candy/plugin-kube, candy/plugin-spice).
//
//   - command:mcp — `charly mcp serve`, the externalized MCP SERVER (the go-sdk bridge that
//     exposes the whole charly CLI as MCP tools). Dispatched by charly fork/exec'ing this
//     binary in CLI mode (sdk.Main → cliMain, command.go), so it owns real terminal stdio:
//     `--stdio` serves the editor/LLM integration over stdin/stdout, `--listen` serves
//     Streamable HTTP (the in-container supervised deployment).
//
// The plugin owns NO podman / OCI-label / port-mapping machinery — the host pre-resolves the
// deployment's declared mcp_provides + the single picked, host-routable dial endpoint
// (preresolveMcpEndpoint, charly/mcp_preresolve.go) and hands them over via the check env, so
// this module needs no container inspection at all.
package mcp

import (
	"context"
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the mcp verb provider (the Invoke dispatch surface).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the plugin's capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

// CliMain is the plugin's CLI entrypoint (command:mcp dispatch — `charly mcp …`).
func CliMain(args []string) int { return cliMain(args) }

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the plugin's ONE gRPC-served capability — verb:mcp (the MCP check verb) —
// plus the self-contained CUE schema, over the wire via sdk.BuildCapabilities. verb:mcp keeps
// its entire authoring contract (the #McpMethod enum + every modifier) on charly's core #Op —
// like cdp/vnc/spice it has NO plugin_input — so it advertises an EMPTY InputDef, and the
// served schema (schema/mcp.cue) exists only to satisfy the host's non-empty-schema load gate.
//
// command:mcp (`charly mcp …`, the externalized MCP-server CLI) is NOT advertised here: it is
// dispatched by charly fork/exec'ing this binary in CLI mode (cliMain), not resolved through
// the gRPC provider registry — so it carries no Describe capability and no plugin_input (its
// args are plain CLI tokens parsed by kong). The candy's plugin.providers declaration still
// lists command:mcp (that drives the CLI-grammar prescan + the baked `.providers` manifest).
//
// The SDK concatenates + compiles the embedded schema/*.cue standalone here, failing loudly
// before serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.178.1200",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "mcp", InputDef: ""},
		},
		schemaFS, "schema")
}
