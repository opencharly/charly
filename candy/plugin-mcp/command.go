package main

import (
	"context"
	"fmt"
	"os"

	"github.com/alecthomas/kong"
)

// command.go is the command:mcp leg of this plugin — the externalized `charly mcp …` CLI,
// ported OUT of charly's core (the deleted charly/mcp_server.go + plugin_command_mcp.go) so
// the github.com/modelcontextprotocol/go-sdk SERVER no longer links into the core binary
// (the operator-approved C1 dep-shed). It is the COMMAND-class companion of this same
// module's verb:mcp check verb (provider.go / methods.go).
//
// Dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly mcp <args…>`, charly RESOLVES this plugin's binary and syscall.Exec's it with the
// pass-through tokens after the `mcp` word, in CLI mode (the go-plugin handshake cookie is
// stripped, so sdk.Main runs cliMain instead of serving gRPC). The plugin therefore owns real
// terminal stdio/TTY — which is what restores `charly mcp serve --stdio` (an out-of-process
// gRPC Invoke could not carry an editor's stdio). The args are parsed with kong against the
// SAME command grammar the core command formerly carried and dispatched to `serve`.

// McpCmdGroup mirrors the original core `charly mcp` command group (charly/mcp_server.go).
// One subcommand: serve.
type McpCmdGroup struct {
	Serve McpServeCmd `cmd:"" help:"Run an MCP server exposing every charly CLI command as a tool"`
}

// McpServeCmd: `charly mcp serve` — the same flag surface the core command carried. The
// flag names derive from the recognized kong `name:` tag (the original's `long:` tag is
// NOT a kong tag — kong ignored it and kebab-cased the field name, which happens to match;
// the explicit `name:` is the correct, self-documenting form).
//
// Unlike the core original, this command does NOT redefine --dir / --repo: the top-level
// `charly --repo OWNER/REPO mcp serve` still pins the project, and the project context is
// threaded to every charly fork/exec as a managed prefix (serve.go computeProjectPrefix).
// --no-default-repo stays, opting out of the auto-fallback to opencharly/charly.
type McpServeCmd struct {
	Listen        string `name:"listen" default:":18765" help:"TCP listen address for Streamable HTTP transport"`
	Path          string `name:"path" default:"/mcp" help:"HTTP path prefix for the MCP endpoint"`
	Stdio         bool   `name:"stdio" help:"Use stdio transport instead of HTTP (for editor/LLM integration)"`
	ReadOnly      bool   `name:"read-only" help:"Skip registration of tools that mutate state"`
	NoDefaultRepo bool   `name:"no-default-repo" help:"Disable auto-fallback to opencharly/charly; require --dir / --repo / local charly.yml"`
}

// cliMain is the CLI-mode entry point (sdk.Main calls it when charly fork/exec'd this plugin
// as a command passthrough). It parses the pass-through tokens against McpCmdGroup and
// dispatches the selected command. `serve` BLOCKS for the lifetime of the MCP server — and
// because the plugin IS the process (syscall.Exec replaced charly), the server's stdio/HTTP
// listener is bound directly to charly's inherited streams/sockets. Returns the process exit
// code.
func cliMain(args []string) int {
	var grp McpCmdGroup
	parser, err := kong.New(&grp,
		kong.Name("mcp"),
		kong.Description("charly mcp — the externalized MCP server bridge"),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin-mcp: build kong parser: %v\n", err)
		return 1
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin-mcp: parse `charly mcp %v`: %v\n", args, err)
		return 1
	}
	switch kctx.Command() {
	case "serve":
		if err := runServe(context.Background(), &grp.Serve); err != nil {
			fmt.Fprintf(os.Stderr, "plugin-mcp serve: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "plugin-mcp: unsupported command %q\n", kctx.Command())
		return 1
	}
}
