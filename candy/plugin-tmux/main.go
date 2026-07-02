// Command plugin-tmux is the OUT-OF-TREE charly COMMAND-class plugin serving the
// externalized `charly tmux …` CLI — the persistent-tmux-session manager ported OUT of
// charly's core (the deleted charly/tmux.go + charly/plugin_command_tmux.go). It is the
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
package main

import (
	"embed"

	"github.com/opencharly/charly/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// main is dual-mode (sdk.Main). For a command-only plugin, charly only ever fork/execs the
// binary (CLI mode) — it never launches it in go-plugin serve mode — so cliMain is the live
// path; the serve half is inert (no gRPC capability).
func main() { sdk.Main(&provider{}, &meta{}, cliMain) }
