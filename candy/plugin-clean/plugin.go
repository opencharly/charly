// Package clean is the charly plugin OWNING the externalized `charly clean` command — the
// build-artifact retention/prune surface. The plugin owns the flag grammar, the category
// orchestration, and the output; the SHARED retention engine (image-tag / build-candy / check-run
// pruning, also called by `charly box build` / `charly check run` / `charly box list tags`) stays in
// core and is reached via the generic "retention" HostBuild seam. There is no hidden core-command
// forward — the plugin does the work, calling back for the one thing it can't compute (the project
// config + the core image inventory), the doctrine the vm + pod deploy plugins established.
//
// clean is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) (provider.go) runs in charly's
// process and gets the in-proc reverse channel that dispatchInProcCommand threads (Seam A), so
// HostBuild("retention") reaches the host engine. The out-of-process placement fork/execs the binary
// → CliMain, which has NO reverse channel and so errors — clean cannot run out-of-process (it needs
// the retention host seam). NewProvider()/NewMeta()/CliMain are the standard dual-mode command shape
// (mirror candy/plugin-migrate); NewMeta advertises command:clean so the compiled-in registry path
// (registerCompiledPlugin → resolve(ClassCommand,"clean") → dispatchInProcCommand) dispatches it.
package clean

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// NewProvider returns the clean provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:clean — the COMPILED-IN registry path resolves it (registerCompiledPlugin
// → providerRegistry.resolve(ClassCommand,"clean") → dispatchInProcCommand → Invoke(OpRun) with the
// threaded in-proc reverse channel) — plus the self-contained doc schema, via sdk.NewMeta.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.181.0001",
		[]sdk.ProvidedCapability{{Class: "command", Word: "clean"}},
		nil)
}

// CliMain is the out-of-process CLI entrypoint (only reached when clean is NOT compiled in). clean
// reaches the shared retention engine via the HostBuild reverse channel, which is unavailable
// out-of-process, so runCleanCLI (with a nil executor) errors clearly; the canonical placement is
// compiled-in (Invoke → provider.go), where the reverse channel is threaded.
func CliMain(args []string) int {
	if err := runCleanCLI(context.Background(), nil, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
