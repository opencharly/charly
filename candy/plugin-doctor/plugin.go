// Package doctor is the charly plugin serving the externalized `charly doctor` command — the
// host-dependency-status surface. It is an importable dual-placement command plugin: the SAME
// NewProvider()/NewMeta()/CliMain compile INTO charly in-process when listed in compiled_plugins,
// or cmd/serve serves them OUT-OF-PROCESS (charly fork/execs the binary for command:doctor
// dispatch) when they are not — placement is invisible above the registry.
//
// The FIFTH WELDED-command externalization in the core-externalization program (after
// candy/plugin-tmux, candy/plugin-preempt, candy/plugin-feature, and candy/plugin-vm). `charly
// doctor` is WELDED to core — its DoctorCmd.Run handler (charly/doctor.go) calls package-main
// host-detection symbols: credentialHealth; DetectGPU / DetectAMDGPU / GPURunArgs /
// DetectHostDevices (devices.go); DetectVFIO / VfioGroupAccessible / MemlockLimitBytes (gpu/vfio).
// None of those can cross the process boundary, so doctor.go MUST STAY CORE (R3). The SOLUTION
// (the vm precedent — re-home the whole leaf onto a hidden core command and raw-forward to it): core
// re-homes DoctorCmd onto the hidden `charly __doctor` command, and this plugin is a THIN FORWARDER
// that raw-forwards the pass-through tokens to `charly __doctor <args…>` (command.go). `doctor` is a
// flags-only LEAF (no subcommands), so the plugin forwards raw args rather than re-expressing a
// grammar — the simplest welded-command shape. No core symbol crosses the boundary; no ad-hoc
// podman/virsh.
//
// CLI dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly doctor <args…>`, charly RESOLVES this plugin's binary (host-built from source, or baked
// into /usr/lib/charly/plugins via pkg/arch) and syscall.Exec's it with the pass-through tokens
// after the `doctor` word, in CLI mode (the go-plugin handshake cookie is stripped, so sdk.Main
// runs cliMain instead of serving gRPC) with CHARLY_BIN stamped to charly's own executable. cliMain
// then syscall.Exec's `charly __doctor <args…>`, so the in-core DoctorCmd runs in the re-entered
// charly process and inherits charly's stdin/stdout/stderr/TTY natively.
//
// A command is NOT a gRPC-registry capability (charly fork/execs the binary; it never connects over
// gRPC for a command), so this plugin advertises NO Describe capability — its serve half (sdk.Serve,
// never reached for a command-only plugin) exists only to satisfy the dual-mode sdk.Main signature.
// The candy's plugin.providers declaration still lists command:doctor (that drives the CLI-grammar
// prescan + the baked `.providers` manifest).
package doctor

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the doctor provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises NO gRPC capability — command:doctor is CLI-dispatched, not resolved
// through the gRPC provider registry — shipping only the self-contained doc schema to satisfy
// the host's non-empty-schema load gate + params codegen loop, via sdk.NewMeta → BuildCapabilities.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.181.0001",
		[]sdk.ProvidedCapability{},
		schemaFS)
}

// CliMain is the plugin's CLI entrypoint (command:doctor dispatch).
func CliMain(args []string) int { return cliMain(args) }
