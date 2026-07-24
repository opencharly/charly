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
// HostBuild kind, returning the per-step results the CheckCmd handlers format, using the
// package-level cmdCtx (valid for the whole `charly check ...` command dispatch). cmdExec is nil
// on the out-of-process CliMain path (no reverse channel) → a clear error.
func hostCheckRun(req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	return hostCheckRunCtx(cmdCtx, req)
}

// hostCheckRunCtx is hostCheckRun with an EXPLICIT ctx — the seam harness_loop.go's scoreLive
// needs (a watchdog probe's own bounded context, not the package-level cmdCtx that spans the
// whole command dispatch). Both entry points route through this SAME Mode switch (R3 — one
// dispatch, not two).
//
// K1-unblock wave: dispatch is now COMPLETE. Mode:"box"/"live"/"feature-live"/"score" dispatch to
// this plugin's OWN pluginCheckRunBox/Live/FeatureLive/Score bodies. Mode:"preflight" forwards to
// the host's "check-run" HostBuild arm (charly/host_build_check_run.go's hostCheckRunPreflight) —
// the ONE surviving host-anchored body, kept there because its image-ensure leg
// (EnsureImagePresent) needs the project *Config + BuildCmd's local-build fallback, both deeply
// host/loader-coupled with no sdk-portable equivalent (see that file's header comment). A nominal
// "feature-box" mode exists in the wire enum but has ZERO callers through this seam (`charly box
// feature run` calls the CLI-free hostFeatureBox engine directly — see feature_run_gather.go's
// header), so it is deliberately NOT cased here — reaching it would be a caller bug, not a
// routable mode. The former dual-mode fallback (a bare default forwarding EVERY uncased mode to
// the host) is retired: every mode is now an explicit case or an explicit unknown-mode error.
func hostCheckRunCtx(ctx context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	if cmdExec == nil {
		return kit.CheckRunReply{}, fmt.Errorf("charly check requires compiled-in placement (the check-run host seam is unavailable out-of-process)")
	}
	switch req.Mode {
	case "box":
		return pluginCheckRunBox(cmdExec, ctx, req)
	case "live":
		return pluginCheckRunLive(cmdExec, ctx, req)
	case "feature-live":
		return pluginCheckRunFeatureLive(cmdExec, ctx, req)
	case "score":
		return pluginCheckRunScore(cmdExec, ctx, req)
	case "preflight":
		return pluginCheckRunPreflight(cmdExec, ctx, req)
	}
	return kit.CheckRunReply{}, fmt.Errorf("check-run: unknown mode %q", req.Mode)
}

// pluginCheckRunPreflight forwards the "preflight" mode to the host's "check-run" HostBuild seam —
// see hostCheckRunCtx's header for why this ONE mode stays host-anchored (EnsureImagePresent's
// *Config/BuildCmd coupling).
func pluginCheckRunPreflight(ex *sdk.Executor, ctx context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	out, err := ex.HostBuild(ctx, "check-run", reqJSON)
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

// hostRetention runs the SHARED check-run prune engine, now owned by candy/plugin-clean
// (K1-alpha core-minimization relocation — retention.go, reached via verb:retention). The
// harness dispatcher defers a {Check:true, Dir} call so `.check/<name>/` is trimmed to
// keep_check_runs after a run. This plugin (like plugin-clean's own CLI) cannot LoadConfig
// itself, so it FIRST fetches the resolved defaults.keep_check_runs via the small
// "retention-defaults" HostBuild seam (the ONE thing the retention engine genuinely cannot
// compute), then reaches candy/plugin-clean's verb:retention over the PLUGIN↔PLUGIN
// InvokeProvider peer-dispatch leg (F10) with the resolved count filled in. The plugin prints
// the "Pruned N (keep_check_runs=K)" line from reply.CheckPaths/KeepCheckRuns.
func hostRetention(ex *sdk.Executor, ctx context.Context, req spec.RetentionRequest) (spec.RetentionReply, error) {
	defReqJSON, err := json.Marshal(spec.RetentionRequest{Dir: req.Dir})
	if err != nil {
		return spec.RetentionReply{}, err
	}
	defOut, err := ex.HostBuild(ctx, "retention-defaults", defReqJSON)
	if err != nil {
		return spec.RetentionReply{}, err
	}
	var defaults spec.RetentionReply
	if err := json.Unmarshal(defOut, &defaults); err != nil {
		return spec.RetentionReply{}, fmt.Errorf("retention-defaults: decode reply: %w", err)
	}
	req.KeepImages = defaults.KeepImages
	req.KeepCheckRuns = defaults.KeepCheckRuns

	reqJSON, err := json.Marshal(req)
	if err != nil {
		return spec.RetentionReply{}, err
	}
	out, err := ex.InvokeProvider(ctx, "verb", "retention", sdk.OpRun, reqJSON, nil, sdk.InvokeProviderOpts{})
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
