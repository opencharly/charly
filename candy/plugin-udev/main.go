// Command plugin-udev is the OUT-OF-TREE charly COMMAND-class plugin serving the
// externalized `charly udev …` CLI — the GPU-device udev-rule manager ported OUT of
// charly's core (the deleted charly/udev.go + charly/plugin_command_udev.go), so the
// GPU-detection + udev-rule-writing code no longer compiles into the core binary. It is
// the FIRST externalizable-command precedent in the core-externalization program: a PURE
// command-only plugin (no gRPC verb), so it mirrors candy/plugin-example-command, not the
// verb+command candy/plugin-mcp / candy/plugin-secrets.
//
// CLI dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly udev <args…>`, charly RESOLVES this plugin's binary (host-built from source, or
// baked into /usr/lib/charly/plugins via pkg/arch) and syscall.Exec's it with the
// pass-through tokens after the `udev` word, in CLI mode (the go-plugin handshake cookie is
// stripped, so sdk.Main runs cliMain instead of serving gRPC). The plugin therefore owns
// real terminal stdio/TTY — `charly udev install` / `remove` shell out to `sudo tee` /
// `sudo udevadm` and reach the real terminal natively.
//
// A command is NOT a gRPC-registry capability (charly fork/execs the binary; it never
// connects over gRPC for a command), so this plugin advertises NO Describe capability — its
// serve half (sdk.Serve, never reached for a command-only plugin) exists only to satisfy the
// dual-mode sdk.Main signature. The candy's plugin.providers declaration still lists
// command:udev (that drives the CLI-grammar prescan + the baked `.providers` manifest).
package main

import (
	"embed"

	"github.com/opencharly/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// main is dual-mode (sdk.Main). For a command-only plugin, charly only ever fork/execs the
// binary (CLI mode) — it never launches it in go-plugin serve mode — so cliMain is the live
// path; the serve half is inert (no gRPC capability).
func main() { sdk.Main(&provider{}, &meta{}, cliMain) }
