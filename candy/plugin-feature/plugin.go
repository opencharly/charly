// Package feature is the charly plugin OWNING the externalized `charly feature` command — the
// plan-shaped-description inspection surface (list / pending / validate). The plugin owns the
// subcommand grammar + the output formatting; the genuine core subsystem it can't hold — the unified
// LOADER (LoadConfig / ScanCandy — the kernel), the Step plan model, and validatePlanSteps (shared with
// `charly box validate`, R3) — stays core and is reached via the generic "feature" HostBuild seam, which
// enumerates every entity's plan into plain DATA (charly/host_build_feature.go). There is no hidden
// core-command forward. (The Feature RUN verbs — `charly box feature run` / `charly check feature run` —
// stay children of box/check in the core binary, NOT part of this plugin.)
//
// feature is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) (provider.go) runs in charly's
// process and gets the in-proc reverse channel that dispatchInProcCommand threads (Seam A), so
// HostBuild("feature") reaches the host loader. The out-of-process placement fork/execs the binary →
// CliMain, which has NO reverse channel and so errors — feature cannot run out-of-process (it needs the
// feature host seam). NewProvider()/NewMeta()/CliMain are the standard dual-mode command shape (mirror
// candy/plugin-clean); NewMeta advertises command:feature so the compiled-in registry path
// (registerCompiledPlugin → resolve(ClassCommand,"feature") → dispatchInProcCommand) dispatches it.
package feature

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// NewProvider returns the feature provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:feature — the COMPILED-IN registry path resolves it (registerCompiledPlugin
// → resolve(ClassCommand,"feature") → dispatchInProcCommand → Invoke(OpRun) with the threaded in-proc
// reverse channel) — plus the self-contained doc schema, via sdk.NewMeta.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.179.0000",
		[]sdk.ProvidedCapability{{Class: "command", Word: "feature"}},
		nil)
}

// CliMain is the out-of-process CLI entrypoint (only reached when feature is NOT compiled in). feature
// reaches the core loader via the HostBuild reverse channel, which is unavailable out-of-process, so
// runFeatureCLI (with a nil executor) errors clearly; the canonical placement is compiled-in (Invoke →
// provider.go), where the reverse channel is threaded.
func CliMain(args []string) int {
	if err := runFeatureCLI(context.Background(), nil, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
