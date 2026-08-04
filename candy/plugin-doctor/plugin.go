// Package doctor is the charly plugin OWNING the externalized `charly doctor` command — the
// host-dependency-status surface. The plugin owns the command end to end: the flag grammar (--json),
// the entire check list + group orchestration, the pass/warn/fail verdicts, the human + JSON report
// formatting, the exit code, AND the pure host ops it runs itself (binary probes via exec.LookPath /
// exec.Command, file reads via os.Stat / os.ReadFile). There is no hidden core-command forward and,
// since the K5 seam-death of the former "hostprobe" HostBuild kind, no remaining core-only dependency
// at all: the genuine host-HARDWARE facts (hostfacts.go) reach candy/plugin-gpu's verb:gpu and
// candy/plugin-secrets' verb:credential peer-to-peer over sdk.Executor.InvokeProvider — the exact
// pattern the arbiter and candy/plugin-vm already use for the same GPU primitives — and the
// install-hint / device-description / distro data tables are this plugin's own embed
// (data.go/data.yml), not a duplicate-avoidance excuse for staying core-coupled.
//
// doctor is COMPILED-IN (listed in charly/charly.yml compiled_plugins) BECAUSE its Invoke(OpRun)
// (provider.go) needs the in-proc reverse channel — threaded by dispatchInProcCommand ("Seam A") — for
// its sdk.Executor.InvokeProvider calls. The out-of-process CliMain path passes a nil executor, so
// those two peer calls degrade to zero values (hostfacts.go's gpuProbe/gatherCredentialHealth) rather
// than erroring — the report still renders, minus the two peer-plugin-backed sections; the canonical
// placement stays compiled-in. command:doctor dispatches through the COMPILED-IN registry path
// (registerCompiledPlugin → resolve(ClassCommand,"doctor") → dispatchInProcCommand → Invoke(OpRun)
// with the threaded in-proc reverse channel), so NewMeta advertises command:doctor while the served
// CUE schema carries no plugin_input (the args are plain CLI tokens). NewProvider()/NewMeta()/CliMain
// are the standard dual-mode command shape (mirror candy/plugin-clean).
package doctor

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
)

// NewProvider returns the doctor provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:doctor — the COMPILED-IN registry path resolves it (registerCompiledPlugin
// → providerRegistry.resolve(ClassCommand,"doctor") → dispatchInProcCommand → Invoke(OpRun) with the
// threaded in-proc reverse channel) — plus the self-contained doc schema, via sdk.NewMeta.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.181.0001",
		[]sdk.ProvidedCapability{{Class: "command", Word: "doctor"}},
		nil)
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
