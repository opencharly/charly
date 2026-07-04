// Package candy is the charly plugin serving the externalized `charly candy` command — the
// candy-manifest authoring surface (set / add-rpm / add-deb / add-pac / add-aur). It is an
// importable dual-placement command plugin: the SAME NewProvider()/NewMeta()/CliMain compile INTO
// charly in-process when the candy is listed in compiled_plugins, or cmd/serve serves them
// OUT-OF-PROCESS (charly fork/execs the binary for command:candy dispatch) when it is not —
// placement is invisible above the registry.
//
// NOTE: this is the TOP-LEVEL `charly candy` authoring tree (charly/scaffold_cmds.go CandyCmd) —
// NOT `charly new candy` (NewCandyCmd, a child of `charly new`), which is a different command and
// stays a builtin.
//
// One of cutover C15's four remaining WELDED-command externalizations (charly
// clean/settings/candy/version), after candy/plugin-tmux, plugin-preempt, plugin-feature,
// plugin-vm, and plugin-doctor. `charly candy` is WELDED to core — its CandyCmd subtree
// (set / add-{rpm,deb,pac,aur}, charly/scaffold_cmds.go) mutates candy/<name>/charly.yml through
// the yaml.v3 Node API (SetByDotPath / appendCandyPackages, comment-preserving) behind the
// resolveProjectFile path-traversal guard — project-authoring machinery an out-of-process plugin
// cannot reach. So CandyCmd MUST STAY CORE (R3). The SOLUTION (the vm precedent — re-home the whole
// subtree onto a hidden core command and raw-forward to it): core re-homes CandyCmd onto the hidden
// `charly __candy` command, and this plugin is a THIN FORWARDER that raw-forwards the pass-through
// tokens to `charly __candy <args…>` (command.go). `candy` is a command TREE (set / add-rpm /
// add-deb / add-pac / add-aur), so the plugin raw-forwards every subcommand token through kong
// passthrough — ONE forwarder covers the whole tree, exactly as candy/plugin-vm forwards the nested
// __vm tree. No core symbol crosses the boundary.
//
// CLI dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly candy <args…>`, charly RESOLVES this plugin's binary (host-built from source, or baked
// into /usr/lib/charly/plugins via pkg/arch) and syscall.Exec's it with the pass-through tokens
// after the `candy` word, in CLI mode (the go-plugin handshake cookie is stripped, so sdk.Main runs
// cliMain instead of serving gRPC) with CHARLY_BIN stamped to charly's own executable. cliMain then
// syscall.Exec's `charly __candy <args…>`, so the in-core CandyCmd runs in the re-entered charly
// process and inherits charly's stdin/stdout/stderr/TTY natively.
//
// A command is NOT a gRPC-registry capability (charly fork/execs the binary; it never connects over
// gRPC for a command), so this plugin advertises NO Describe capability — its serve half (sdk.Serve,
// never reached for a command-only plugin) exists only to satisfy the dual-mode sdk.Main signature.
// The candy's plugin.providers declaration still lists command:candy (that drives the CLI-grammar
// prescan + the baked `.providers` manifest).
package candy

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the candy provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises NO gRPC capability — command:candy is CLI-dispatched, not resolved
// through the gRPC provider registry — shipping only the self-contained doc schema to satisfy
// the host's non-empty-schema load gate + params codegen loop, via sdk.NewMeta → BuildCapabilities.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.181.0001",
		[]sdk.ProvidedCapability{},
		schemaFS)
}

// CliMain is the plugin's CLI entrypoint (command:candy dispatch).
func CliMain(args []string) int { return cliMain(args) }
