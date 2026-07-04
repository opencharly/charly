// Package tmux is the charly plugin serving the externalized `charly tmux …` command — the
// persistent-tmux-session manager. It is an importable dual-placement command plugin: the SAME
// NewProvider()/NewMeta()/CliMain compile INTO charly in-process when listed in compiled_plugins,
// or cmd/serve serves them OUT-OF-PROCESS (charly fork/execs the binary for command:tmux dispatch)
// when they are not — placement is invisible above the registry. It is the
// FIRST WELDED-command externalization in the core-externalization program: unlike udev
// (self-contained, stdlib + x/sys/unix), `charly tmux` was WELDED to the core
// venue/executor resolver (resolveCheckVenue + the DeployExecutor RunCapture
// path). The resolver STAYS core (12 callers); this plugin re-expresses each of the 8
// tmux leaves as a shell-back through SANCTIONED `charly` CLI verbs — `charly cmd <box>
// 'tmux …'` (non-interactive) and `charly shell <box> -c 'tmux …'` (interactive) — so no
// core symbol crosses the process boundary and no ad-hoc podman is used (R4). It mirrors
// candy/plugin-example-command / candy/plugin-udev (a pure command-only plugin, no gRPC verb).
//
// CLI dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly tmux <args…>`, charly RESOLVES this plugin's binary (host-built from source, or
// baked into /usr/lib/charly/plugins via pkg/arch) and syscall.Exec's it with the
// pass-through tokens after the `tmux` word, in CLI mode (the go-plugin handshake cookie is
// stripped, so sdk.Main runs cliMain instead of serving gRPC) with CHARLY_BIN stamped to
// charly's own executable. The plugin owns real terminal stdio/TTY — `charly tmux shell` /
// `attach` shell back through `charly shell` and the interactive TTY flows natively.
//
// A command is NOT a gRPC-registry capability (charly fork/execs the binary; it never
// connects over gRPC for a command), so this plugin advertises NO Describe capability — its
// serve half (sdk.Serve, never reached for a command-only plugin) exists only to satisfy the
// dual-mode sdk.Main signature. The candy's plugin.providers declaration still lists
// command:tmux (that drives the CLI-grammar prescan + the baked `.providers` manifest).
package tmux

import (
	"embed"

	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the tmux provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the plugin's capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

// CliMain is the plugin's CLI entrypoint (command:tmux dispatch).
func CliMain(args []string) int { return cliMain(args) }
