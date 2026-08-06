// Package feature is the charly plugin OWNING the externalized `charly feature` command — the
// plan-shaped-description inspection surface (list / pending / validate). The plugin owns the
// subcommand grammar + the output formatting AND the project ENUMERATION (command.go: the former
// "feature" HostBuild seam's body — charly/host_build_feature.go, DELETED K-wave 2 cone R6 — now
// loads the project PLUGIN-SIDE over the reverse channel via loaderkit: LoadUnifiedViaExecutor +
// ProjectCandiesScanned + FinalizeScannedCandies, since the loader is plugin-reachable). The Step
// plan model + validatePlanSteps are spec/kit-shared (validatePlanSteps also serves `charly box
// validate`, R3). There is no hidden core-command forward. (The Feature RUN verbs — `charly box
// feature run` / `charly check feature run` — stay children of box/check in the core binary, NOT
// part of this plugin.)
//
// feature is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) (provider.go) runs in charly's
// process and gets the in-proc reverse channel that dispatchInProcCommand threads (Seam A), so the
// loader resolve reaches the host loader legs. The out-of-process placement fork/execs the binary →
// CliMain, which has NO reverse channel and so errors — feature cannot run out-of-process (it needs the
// loader reverse channel). NewProvider()/NewMeta()/CliMain are the standard dual-mode command shape
// (mirror candy/plugin-clean); NewMeta advertises command:feature so the compiled-in registry path
// (registerCompiledPlugin → resolve(ClassCommand,"feature") → dispatchInProcCommand) dispatches it.
package feature

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
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
// reaches the loader via the reverse channel, which is unavailable out-of-process, so runFeatureCLI
// (with a nil executor) errors clearly; the canonical placement is compiled-in (Invoke →
// provider.go), where the reverse channel is threaded.
func CliMain(args []string) int {
	if err := runFeatureCLI(context.Background(), nil, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
