// Package doctor is the charly plugin OWNING the externalized `charly doctor` command — the
// host-dependency-status surface. The plugin owns the command end to end: the flag grammar (--json),
// the entire check list + group orchestration, the pass/warn/fail verdicts, the human + JSON report
// formatting, the exit code, AND the pure host ops it runs itself (binary probes via exec.LookPath /
// exec.Command, file reads via os.Stat / os.ReadFile). No plugin-specific report LOGIC is left in core;
// there is no hidden core-command forward — the plugin does the work, calling back only for the one
// thing it cannot compute (the genuine host-hardware facts), the doctrine the clean + settings command
// plugins established.
//
// The ONE thing the plugin cannot compute itself is the genuine host-HARDWARE subsystem + core-owned
// data: the GPU/VFIO/device detection primitives (the C11 shims, multi-caller with the vm/deploy
// paths), the credential-store health (verb:credential, which lazy-connects host-side), and the core
// install-hint / device-description tables. Those STAY core (duplicating them into the plugin would
// violate R3) and are reached via the generic "hostprobe" HostBuild seam
// (charly/host_build_hostprobe.go, spec.HostProbeRequest → spec.HostProbeReply), which runs the
// detection primitives host-side and returns RAW FACTS ONLY — zero formatting or verdict logic crosses
// into core. "hostprobe" is a class-generic action noun, not a provider word (F11).
//
// doctor is COMPILED-IN (listed in charly/charly.yml compiled_plugins) BECAUSE its Invoke(OpRun)
// (provider.go) needs the in-proc reverse channel — threaded by dispatchInProcCommand ("Seam A") — to
// call HostBuild("hostprobe"). The out-of-process CliMain path has no reverse channel, so it errors:
// doctor cannot run out-of-process, it needs the hostprobe host seam. command:doctor dispatches through
// the COMPILED-IN registry path (registerCompiledPlugin → resolve(ClassCommand,"doctor") →
// dispatchInProcCommand → Invoke(OpRun) with the threaded in-proc reverse channel), so NewMeta
// advertises command:doctor while the served CUE schema carries no plugin_input (the args are plain CLI
// tokens). NewProvider()/NewMeta()/CliMain are the standard dual-mode command shape (mirror
// candy/plugin-clean).
package doctor

import (
	"context"
	"embed"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the doctor provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:doctor — the COMPILED-IN registry path resolves it (registerCompiledPlugin
// → providerRegistry.resolve(ClassCommand,"doctor") → dispatchInProcCommand → Invoke(OpRun) with the
// threaded in-proc reverse channel) — plus the self-contained doc schema, via sdk.NewMeta.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.181.0001",
		[]sdk.ProvidedCapability{{Class: "command", Word: "doctor"}},
		schemaFS)
}

// CliMain is the out-of-process CLI entrypoint (only reached when doctor is NOT compiled in). doctor
// reaches the "hostprobe" host seam via the HostBuild reverse channel, which is unavailable
// out-of-process, so runDoctorCLI (with a nil executor) errors clearly; the canonical placement is
// compiled-in (Invoke → provider.go), where the reverse channel is threaded.
func CliMain(args []string) int {
	if err := runDoctorCLI(context.Background(), nil, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
