// Package clean is the charly plugin serving the externalized `charly clean` command — the
// build-artifact retention/prune surface. It is an importable dual-placement command plugin: the
// SAME NewProvider()/NewMeta()/CliMain compile INTO charly in-process when listed in
// compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS (charly fork/execs the binary for
// command:clean dispatch) when they are not — placement is invisible above the registry.
//
// One of cutover C15's four remaining WELDED-command externalizations (charly
// clean/settings/candy/version), after candy/plugin-tmux, plugin-preempt, plugin-feature,
// plugin-vm, and plugin-doctor. `charly clean` is WELDED to core — its CleanCmd.Run handler
// (charly/clean.go) reads the project charly.yml `defaults:` (keep_images / keep_check_runs),
// resolves the build engine (ResolveRuntime), and prunes .build/ / .check/ artifacts + the
// charly-labeled podman image tags — project + engine + filesystem machinery an out-of-process
// plugin cannot reach. So clean.go MUST STAY CORE (R3). The SOLUTION (the vm/doctor precedent —
// re-home the leaf onto a hidden core command and raw-forward to it): core re-homes CleanCmd onto
// the hidden `charly __clean` command, and this plugin is a THIN FORWARDER that raw-forwards the
// pass-through tokens to `charly __clean <args…>` (command.go). `clean` is a flags-only LEAF (no
// subcommands), so the plugin forwards raw args rather than re-expressing a grammar — the simplest
// welded-command shape. No core symbol crosses the boundary; no ad-hoc podman.
//
// CLI dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly clean <args…>`, charly RESOLVES this plugin's binary (host-built from source, or baked
// into /usr/lib/charly/plugins via pkg/arch) and syscall.Exec's it with the pass-through tokens
// after the `clean` word, in CLI mode (the go-plugin handshake cookie is stripped, so sdk.Main
// runs cliMain instead of serving gRPC) with CHARLY_BIN stamped to charly's own executable.
// cliMain then syscall.Exec's `charly __clean <args…>`, so the in-core CleanCmd runs in the
// re-entered charly process and inherits charly's stdin/stdout/stderr/TTY natively.
//
// A command is NOT a gRPC-registry capability (charly fork/execs the binary; it never connects over
// gRPC for a command), so this plugin advertises NO Describe capability — its serve half (sdk.Serve,
// never reached for a command-only plugin) exists only to satisfy the dual-mode sdk.Main signature.
// The candy's plugin.providers declaration still lists command:clean (that drives the CLI-grammar
// prescan + the baked `.providers` manifest).
package clean

import (
	"embed"

	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the clean provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the plugin's capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

// CliMain is the plugin's CLI entrypoint (command:clean dispatch).
func CliMain(args []string) int { return cliMain(args) }
