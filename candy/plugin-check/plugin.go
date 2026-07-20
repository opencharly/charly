// Package check is the charly plugin OWNING the externalized `charly check` command family (P12):
// the box/live/run/feature evaluation surface + the AI-iteration harness / R10 bed-runner. The
// plugin owns the CLI grammar, the plan gathering, and the output formatting; the composite
// host-serving Mechanisms it cannot perform in a separate module — building the venue executor,
// extracting the OCI-label plan, and dispatching the plan-walk's verbs through the provider
// registry — stay in core and are reached via the generic "check-run" HostBuild seam
// (charly/host_build_check_run.go, spec.CheckRunRequest → kit.CheckRunReply), the config loader +
// deploy ledger via HostBuild("config-resolve"/"config-persist"), the agent CLI via
// InvokeProvider(kind:agent), and the `charly` reentry (the harness shells out to build/deploy/
// check subcommands) via HostBuild("cli"). No plugin-specific command LOGIC is left in core.
//
// check is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) runs in charly's process
// and gets the in-proc reverse channel (dispatchInProcCommand threads it), so the check-run /
// config / cli / agent host seams reach the core engine. The out-of-process CliMain path has no
// reverse channel and so errors — check cannot run out-of-process (it needs the host seams). The
// SAME NewProvider()/NewMeta() compile INTO charly in-process — placement is invisible.
package check

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// NewProvider returns the check provider (command:check).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:check — the COMPILED-IN registry path resolves it
// (registerCompiledPlugin → providerRegistry.resolve(ClassCommand,"check") → dispatchInProcCommand
// → Invoke(OpRun) with the threaded in-proc reverse channel). command:check is input-less (its
// args are plain CLI tokens kong-parsed into the CheckCmd tree), so it ships NO schema — the load
// gate waives it (mirror candy/plugin-clean). Subcommands is DERIVED from CheckCmd's OWN Kong tags
// via sdk.KongSubcommands (F-CLI-NEST) rather than hand-duplicated (R3): the host uses it to build
// a REAL nested Kong grammar (restoring `charly check --help`'s subcommand listing) and to
// synthesize a "check.<name>" leaf per entry for `charly __cli-model` / MCP tool generation — both
// lost when `check`'s CLI moved off a static core Kong struct onto this dynamic dispatch plugin.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.198.2131",
		[]sdk.ProvidedCapability{{Class: "command", Word: "check", Subcommands: sdk.KongSubcommands(&CheckCmd{})}},
		nil)
}

// CliMain is the out-of-process CLI entrypoint (only reached when check is NOT compiled in). check
// reaches the host engine via the HostBuild reverse channel, which is unavailable out-of-process,
// so it errors clearly; the canonical placement is compiled-in (Invoke → provider.go), where the
// reverse channel is threaded (mirror candy/plugin-clean's CliMain).
func CliMain(_ []string) int {
	fmt.Fprintln(os.Stderr, "charly check requires compiled-in placement (the host check-run/config/cli seams are unavailable out-of-process)")
	return 1
}
