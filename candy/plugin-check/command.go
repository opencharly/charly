package check

import (
	"context"
	"encoding/json"
	"fmt"

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
	return sdk.RunInProcCLI("check", &cli, args)
}

// hostCheckRun asks the host to build the venue + run a check plan via the generic "check-run"
// HostBuild kind, returning the per-step results the CheckCmd handlers format. cmdExec is nil on the
// out-of-process CliMain path (no reverse channel) → a clear error.
//
// K1-unblock wave (mid-flight, transitional): Mode:"box"/"live"/"feature-live" now dispatch to
// this plugin's OWN pluginCheckRunBox/Live/FeatureLive instead of the host's "check-run" HostBuild
// arm — three of five LIVE-reachable arms moved (a nominal "feature-box" mode exists in the wire
// enum but has ZERO callers through this seam — `charly box feature run` calls the CLI-free
// hostFeatureBox engine directly — so there is no arm to move there; see feature_run_gather.go's
// header). The remaining two modes ("score", "preflight") still route to the host; this dual-mode
// dispatch is a legal Hard-Cutover mid-flight state (CLAUDE.md "Hard Cutover by Default") and is
// deleted (along with their charly/host_build_check_run.go arms) once every arm has moved, before
// the R10 acceptance run.
func hostCheckRun(req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	if cmdExec == nil {
		return kit.CheckRunReply{}, fmt.Errorf("charly check requires compiled-in placement (the check-run host seam is unavailable out-of-process)")
	}
	switch req.Mode {
	case "box":
		return pluginCheckRunBox(cmdExec, cmdCtx, req)
	case "live":
		return pluginCheckRunLive(cmdExec, cmdCtx, req)
	case "feature-live":
		return pluginCheckRunFeatureLive(cmdExec, cmdCtx, req)
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

// hostRetention runs the SHARED check-run prune engine over the existing "retention" host seam
// (pruneCheckRuns STAYS core — multi-caller: box build / check run / list tags all prune). The
// harness dispatcher defers a {Check:true, Dir} call so `.check/<name>/` is trimmed to
// keep_check_runs after a run; the host resolves the keep-count (Defaults.KeepCheckRuns + the
// fallback) itself, so this seam — not the check projection — owns retention (R3). The plugin prints
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

// checkLoadPlugins triggers the host's UNCHANGED plugin-connect engine (resolveCheckRunnerContext)
// over the thin "check-load-plugins" seam, so any out-of-process verb candy a live plan's steps
// reference is connected (registered in this host process's providerRegistry) BEFORE the plugin
// dispatches those steps via InvokeProvider. Best-effort by design (mirrors the core original's own
// graceful degrade): a connect failure surfaces loudly later, at actual verb dispatch, never here.
func checkLoadPlugins(ex *sdk.Executor, ctx context.Context, name, dir string) {
	reqJSON, err := json.Marshal(spec.CheckLoadPluginsRequest{Name: name, Dir: dir})
	if err != nil {
		return
	}
	_, _ = ex.HostBuild(ctx, "check-load-plugins", reqJSON)
}
