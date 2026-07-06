// Package udev is the charly plugin serving the externalized `charly udev …` command — the
// GPU-device udev-rule manager. It is an importable dual-placement command plugin: the SAME
// NewProvider()/NewMeta()/CliMain compile INTO charly in-process when listed in compiled_plugins,
// or cmd/serve serves them OUT-OF-PROCESS (charly fork/execs the binary for command:udev dispatch)
// when they are not — placement is invisible above the registry. It is
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
package udev

import (
	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// NewProvider returns the udev provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises NO gRPC capability — command:udev is CLI-dispatched, not resolved
// through the gRPC provider registry — shipping only the self-contained doc schema to
// satisfy the host's non-empty-schema load gate (via sdk.NewMeta → BuildCapabilities).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.179.0000",
		[]sdk.ProvidedCapability{},
		nil)
}

// CliMain is the plugin's CLI entrypoint (command:udev dispatch).
func CliMain(args []string) int { return cliMain(args) }
