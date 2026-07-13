package check

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alecthomas/kong"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// command.go — the command:check dispatch + the host-seam bridges. The plugin OWNS the `charly check`
// CLI grammar (the CheckCmd kong tree) + the output formatting; the composite host-serving Mechanisms
// it cannot perform (venue construction + OCI-label plan extraction + registry verb dispatch) stay in
// core behind the generic "check-run" HostBuild seam. command:check is COMPILED-IN and dispatches
// exactly ONE `charly check …` invocation per process, so the reverse-channel executor is stashed in a
// package var at Invoke(OpRun) entry (setCommandContext) — race-free single-command-per-process,
// mirroring candy/plugin-vm.

// cmdCtx / cmdExec carry the Invoke(OpRun) reverse-channel handle to the deep CLI call sites.
var (
	cmdCtx  context.Context
	cmdExec *sdk.Executor
)

// setCommandContext stashes the reverse-channel executor for the duration of one `charly check …`
// dispatch. Called once at the top of command:check's Invoke(OpRun).
func setCommandContext(ctx context.Context, ex *sdk.Executor) {
	cmdCtx = ctx
	cmdExec = ex
}

// dispatchCheckCLI kong-parses the pass-through args into the CheckCmd tree and runs the selected leaf.
func dispatchCheckCLI(args []string) error {
	var cli CheckCmd
	parser, err := kong.New(&cli, kong.Name("check"), kong.Exit(func(int) {}))
	if err != nil {
		return err
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		return err
	}
	return kctx.Run()
}

// hostCheckRun asks the host to build the venue + run a check plan via the generic "check-run"
// HostBuild kind, returning the per-step results the CheckCmd handlers format. cmdExec is nil on the
// out-of-process CliMain path (no reverse channel) → a clear error.
func hostCheckRun(req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	if cmdExec == nil {
		return kit.CheckRunReply{}, fmt.Errorf("charly check requires compiled-in placement (the check-run host seam is unavailable out-of-process)")
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	out, err := cmdExec.HostBuild(cmdCtx, "check-run", reqJSON)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	var reply kit.CheckRunReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("check-run: decode reply: %w", err)
	}
	return reply, nil
}

// bedHostBuild drives one op of the transitional "check-bed" host-session seam (P12 Wave-2): it
// marshals the CheckBedRequest, HostBuild("check-bed")s it over the reverse channel, and decodes
// the CheckBedReply. The AI-harness R10 bed driver (the leaf harness code) calls setup → members-up
// / wait-ready / members-down → teardown through this bridge; the host holds the bed's lock / lease
// / env lifecycle across the driver's many bedCli calls. ex/ctx are passed explicitly (the harness
// owns its executor + context, unlike the single-shot cmdExec the CheckCmd leaves use).
func bedHostBuild(ex *sdk.Executor, ctx context.Context, req spec.CheckBedRequest) (spec.CheckBedReply, error) {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return spec.CheckBedReply{}, err
	}
	out, err := ex.HostBuild(ctx, "check-bed", reqJSON)
	if err != nil {
		return spec.CheckBedReply{}, err
	}
	var reply spec.CheckBedReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return spec.CheckBedReply{}, fmt.Errorf("check-bed: decode reply: %w", err)
	}
	return reply, nil
}

// bedCli runs one `charly <argv>` subcommand host-side via the generic "cli" HostBuild seam
// (hostBuildCli forks os.Args[0] in the host process, inheriting the check-bed session's env). The
// AI-harness bed driver reentrantly shells out every build / deploy / check / update / teardown
// step through this bridge. capture=true captures stdout only (correct for a status / --format yaml
// parse); capture=false inherits the host stdio for an interactive leg.
func bedCli(ex *sdk.Executor, ctx context.Context, capture bool, argv ...string) (spec.CliReply, error) {
	return bedCliReq(ex, ctx, spec.CliRequest{Argv: argv, Capture: capture})
}

// bedCliCombined is bedCli with COMBINED capture (stdout+stderr merged into reply.Stdout) — used for
// the check-bed per-step .log so a `charly check …` child's STDERR-written results are persisted
// (pre-relocation parity: core runCapture captured combined output; plain bedCli captures stdout
// only, which would drop the check results from the log).
func bedCliCombined(ex *sdk.Executor, ctx context.Context, argv ...string) (spec.CliReply, error) {
	return bedCliReq(ex, ctx, spec.CliRequest{Argv: argv, Capture: true, Combined: true})
}

// bedCliReq is the shared cli-seam marshal/dispatch/decode (R3 — one body for bedCli/bedCliCombined).
func bedCliReq(ex *sdk.Executor, ctx context.Context, req spec.CliRequest) (spec.CliReply, error) {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return spec.CliReply{}, err
	}
	out, err := ex.HostBuild(ctx, "cli", reqJSON)
	if err != nil {
		return spec.CliReply{}, err
	}
	var reply spec.CliReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return spec.CliReply{}, fmt.Errorf("cli: decode reply: %w", err)
	}
	return reply, nil
}

// checkConfig resolves the AI-harness's check-project projection over the dedicated "check-config"
// host seam (a plugin, a separate module, cannot LoadUnified). The harness leaves consume the reply's
// bed-vs-iterate classification (IsBed/HasNode/HasIterate), sandbox class, pod-target disposability,
// the resolved iterate: config, the include-expanded plan, and the kind:agent catalog. ex/ctx are
// explicit (the harness owns its executor). Retention rides HostBuild("retention"); this seam carries
// no keep-count. TRANSITIONAL: dies at K1 (post-loaderkit the plugin self-loads the project).
func checkConfig(ex *sdk.Executor, ctx context.Context, req spec.CheckConfigRequest) (spec.CheckConfigReply, error) {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return spec.CheckConfigReply{}, err
	}
	out, err := ex.HostBuild(ctx, "check-config", reqJSON)
	if err != nil {
		return spec.CheckConfigReply{}, err
	}
	var reply spec.CheckConfigReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return spec.CheckConfigReply{}, fmt.Errorf("check-config: decode reply: %w", err)
	}
	return reply, nil
}

// hostRetention runs the SHARED check-run prune engine over the existing "retention" host seam
// (pruneCheckRuns STAYS core — multi-caller: box build / check run / list tags all prune). The
// harness dispatcher defers a {Check:true, Dir} call so `.check/<name>/` is trimmed to
// keep_check_runs after a run; the host resolves the keep-count (Defaults.KeepCheckRuns + the
// fallback) itself, so this seam — not a check-config field — owns retention (R3). The plugin prints
// the "Pruned N (keep_check_runs=K)" line from reply.CheckPaths/KeepCheckRuns.
func hostRetention(ex *sdk.Executor, ctx context.Context, req spec.RetentionRequest) (spec.RetentionReply, error) {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return spec.RetentionReply{}, err
	}
	out, err := ex.HostBuild(ctx, "retention", reqJSON)
	if err != nil {
		return spec.RetentionReply{}, err
	}
	var reply spec.RetentionReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return spec.RetentionReply{}, fmt.Errorf("retention: decode reply: %w", err)
	}
	return reply, nil
}
