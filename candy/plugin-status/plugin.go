// Package status is the charly plugin OWNING the externalized `charly status` command — the
// runtime-status get/render surface (table / detail / JSON, --all, --nested). The plugin owns the
// subcommand grammar (command.go), the PURE nested-overlay fold (overlay.go), and the render
// output (render.go); the collection engine (Collector, the per-substrate collectors, and the
// declared-nested-tree pre-resolution) stays in core and is reached via the generic
// "status-substrate" HostBuild seam. There is no hidden core-command forward — the plugin does
// the work, calling back for the ONE thing it cannot do itself (live host/venue collection), the
// doctrine candy/plugin-settings established.
//
// status is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) (provider.go) runs in
// charly's process and gets the in-proc reverse channel that dispatchInProcCommand threads (Seam
// A), so HostBuild("status-substrate") reaches the host collection engine. The out-of-process
// placement fork/execs the binary → CliMain, which has NO reverse channel and so errors —
// status cannot run out-of-process (it needs the status-substrate host seam). NewProvider()/
// NewMeta()/CliMain are the standard dual-mode command shape (mirror candy/plugin-settings);
// NewMeta advertises command:status so the compiled-in registry path (registerCompiledPlugin →
// resolve(ClassCommand,"status") → dispatchInProcCommand) dispatches it.
//
// provider.go ALSO handles sdk.OpStatusCollect — the programmatic status-collection API
// (distinct from the lifecycle OpStatus a substrate plugin serves): HostBuild("status-substrate")
// → the PURE overlay → the overlaid []spec.DeploymentStatus as ResultJson (no render). Any
// in-process peer can InvokeProvider(class:command, word:status, op:sdk.OpStatusCollect) to get
// structured status without shelling out to `charly status --json`.
package status

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// calver is this plugin candy's CalVer identity (matches charly.yml version:).
const calver = "2026.194.1600"

// NewProvider returns the status provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:status — the COMPILED-IN registry path resolves it
// (registerCompiledPlugin → resolve(ClassCommand,"status") → dispatchInProcCommand →
// Invoke(OpRun) with the threaded in-proc reverse channel) — plus the self-contained doc schema
// (nil: command:status is input-less — the args are plain CLI tokens, never a `plugin_input`
// envelope), via sdk.NewMeta.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "command", Word: "status"}},
		nil)
}

// CliMain is the out-of-process CLI entrypoint (only reached when status is NOT compiled in).
// status reaches the collection engine via the HostBuild reverse channel, which is unavailable
// out-of-process, so runStatusCLI (with a nil executor) errors clearly; the canonical placement
// is compiled-in (Invoke → provider.go), where the reverse channel is threaded.
func CliMain(args []string) int {
	if err := runStatusCLI(context.Background(), nil, args); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
