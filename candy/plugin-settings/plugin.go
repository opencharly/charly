// Package settings is the charly plugin serving the externalized `charly settings` command — the
// runtime-configuration get/set/list/path/reset surface. It is an importable dual-placement command
// plugin: the SAME NewProvider()/NewMeta()/CliMain compile INTO charly in-process when listed in
// compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS (charly fork/execs the binary for
// command:settings dispatch) when they are not — placement is invisible above the registry.
//
// One of cutover C15's four remaining WELDED-command externalizations (charly
// clean/settings/candy/version), after candy/plugin-tmux, plugin-preempt, plugin-feature,
// plugin-vm, and plugin-doctor. `charly settings` is WELDED to core — its SettingsCmd subtree
// (get/set/list/path/reset, charly/main.go) reads and writes the runtime config file
// ~/.config/charly/config.yml (GetConfigValue / SetConfigValue / ListConfigValues /
// ResetConfigValue / RuntimeConfigPath) and resolves the credential-store backend + the runtime
// engine — config machinery an out-of-process plugin cannot reach. So SettingsCmd MUST STAY CORE
// (R3). The SOLUTION (the vm precedent — re-home the whole subtree onto a hidden core command and
// raw-forward to it): core re-homes SettingsCmd onto the hidden `charly __settings` command, and
// this plugin is a THIN FORWARDER that raw-forwards the pass-through tokens to
// `charly __settings <args…>` (command.go). `settings` is a command TREE (get/set/list/path/reset),
// so the plugin raw-forwards every subcommand token through kong passthrough — ONE forwarder covers
// the whole tree, exactly as candy/plugin-vm forwards the nested __vm tree. No core symbol crosses
// the boundary.
//
// CLI dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly settings <args…>`, charly RESOLVES this plugin's binary (host-built from source, or baked
// into /usr/lib/charly/plugins via pkg/arch) and syscall.Exec's it with the pass-through tokens
// after the `settings` word, in CLI mode (the go-plugin handshake cookie is stripped, so sdk.Main
// runs cliMain instead of serving gRPC) with CHARLY_BIN stamped to charly's own executable. cliMain
// then syscall.Exec's `charly __settings <args…>`, so the in-core SettingsCmd runs in the re-entered
// charly process and inherits charly's stdin/stdout/stderr/TTY natively.
//
// A command is NOT a gRPC-registry capability (charly fork/execs the binary; it never connects over
// gRPC for a command), so this plugin advertises NO Describe capability — its serve half (sdk.Serve,
// never reached for a command-only plugin) exists only to satisfy the dual-mode sdk.Main signature.
// The candy's plugin.providers declaration still lists command:settings (that drives the CLI-grammar
// prescan + the baked `.providers` manifest).
package settings

import (
	"embed"

	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the settings provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the plugin's capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

// CliMain is the plugin's CLI entrypoint (command:settings dispatch).
func CliMain(args []string) int { return cliMain(args) }
