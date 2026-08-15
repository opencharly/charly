package check

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// command.go — the command:check dispatch + the host-seam bridges. The plugin OWNS the `charly check`
// CLI grammar (the CheckCmd kong tree) + the output formatting; the composite host-serving Mechanisms
// it cannot perform (venue construction + OCI-label plan extraction + registry verb dispatch) stay in
// core, reached over the remaining HostBuild seams (cli / check-load-plugins /
// check-bed-gpu-prereq) + InvokeProvider peer dispatch — the former generic "check-run" HostBuild
// seam is DELETED (K-wave 2 cone R4: every mode now dispatches to this plugin's OWN bodies, see
// hostCheckRunCtx below), and the post-run prune's retention-defaults resolve moved plugin-side
// (K-wave 2 cone R6, loaderkit.ResolveRetentionDefaultsViaExecutor). command:check is COMPILED-IN
// and dispatches exactly ONE `charly check …`
// invocation per process, so the reverse-channel executor is stashed in a package var at
// Invoke(OpRun) entry (setCommandContext) — race-free single-command-per-process, mirroring
// candy/plugin-vm.

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

// hostCheckRun dispatches a check plan to this package's OWN per-mode bodies (hostCheckRunCtx),
// returning the per-step results the CheckCmd handlers format, using the
// package-level cmdCtx (valid for the whole `charly check ...` command dispatch). cmdExec is nil
// on the out-of-process CliMain path (no reverse channel) → a clear error.
// Package-level var for the same reason live_image.go's containerImageRef is one: it lets a test
// exercise a CLI arm's OUTPUT decisions (which lines it prints, on which paths) without a live
// container store or a reverse channel. Override THIS var, never the per-mode bodies.
var hostCheckRun = func(req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	return hostCheckRunCtx(cmdCtx, req)
}

// hostCheckRunCtx is hostCheckRun with an EXPLICIT ctx — the seam harness_loop.go's scoreLive
// needs (a watchdog probe's own bounded context, not the package-level cmdCtx that spans the
// whole command dispatch). Both entry points route through this SAME Mode switch (R3 — one
// dispatch, not two).
//
// K-wave 2 cone R4: dispatch is COMPLETE — the "check-run" HostBuild kind is DELETED. Every mode
// dispatches to this plugin's OWN bodies: Mode:"box"/"live"/"feature-live"/"score" →
// pluginCheckRunBox/Live/FeatureLive/Score; Mode:"preflight" → pluginCheckRunPreflight (the
// relocated preflight body — loaderkit.LoadUnifiedViaExecutor + InvokeProvider("build","ensure"),
// see its doc below). The "feature-box" mode is the BUILD-scope `charly box feature run` engine
// (pluginCheckRunFeatureBox, feature_box_gather.go — relocated from core hostFeatureBox in
// cone-C #31): candy/plugin-box's command:feature InvokeProvider's command:check's hidden
// `__feature-box` leaf, which routes here. Every mode is an explicit case or an explicit
// unknown-mode error.
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
	case "feature-box":
		return pluginCheckRunFeatureBox(cmdExec, ctx, req)
	case "score":
		return pluginCheckRunScore(cmdExec, ctx, req)
	case "preflight":
		return pluginCheckRunPreflight(cmdExec, ctx, req)
	}
	return kit.CheckRunReply{}, fmt.Errorf("check-run: unknown mode %q", req.Mode)
}

// pluginCheckRunPreflight performs the host-target image preflight plugin-side (K-wave 2 cone R4 —
// the "check-run" HostBuild kind is DELETED; its last arm "preflight" relocated here). It loads the
// project via loaderkit (LoadUnifiedViaExecutor — the same self-load bed_session.go uses), checks
// the entity exists, filters agent-provisioned venues via the fleet-tree predicate
// spec.VenueIsAgentProvisioned (the former host-anchoring rationale — the plugin CAN read the
// fleet tree, so the filter is fully portable), and ensures every remaining candidate image is
// present in local podman storage via the compiled-in candy/plugin-build build:ensure word
// (InvokeProvider peer dispatch — the same leg core's dispatchBuildEnsure drives).
func pluginCheckRunPreflight(ex *sdk.Executor, ctx context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	dir := req.Dir
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, ex, dir)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if !ok || uf == nil {
		return kit.CheckRunReply{}, fmt.Errorf("check-run preflight: no charly.yml in %s", dir)
	}
	if _, has := uf.Fleet[req.Name]; !has {
		return kit.CheckRunReply{}, fmt.Errorf("check-run preflight: no entity %q in %s", req.Name, dir)
	}
	fmt.Fprintf(os.Stderr, "preflight: ensuring %d image(s) present in podman storage\n", len(req.Filter))
	for _, ref := range req.Filter {
		if spec.VenueIsAgentProvisioned(uf, ref) {
			continue
		}
		params, merr := json.Marshal(spec.BuildEnsureRequest{Image: ref, Dir: dir})
		if merr != nil {
			return kit.CheckRunReply{}, merr
		}
		out, ierr := ex.InvokeProvider(ctx, "build", "ensure", sdk.OpBuild, params, nil, sdk.InvokeProviderOpts{})
		if ierr != nil {
			return kit.CheckRunReply{}, fmt.Errorf("preflight: %w", ierr)
		}
		var reply spec.BuildEnsureReply
		if len(out) > 0 {
			if derr := json.Unmarshal(out, &reply); derr != nil {
				return kit.CheckRunReply{}, fmt.Errorf("preflight: decode ensure reply: %w", derr)
			}
		}
		if reply.Error != "" {
			return kit.CheckRunReply{}, fmt.Errorf("%s", reply.Error)
		}
	}
	return kit.CheckRunReply{}, nil
}

// bedHostBuild DIED (#55 W3 B2-full): the "check-bed" HostBuild seam it bridged to is gone. The
// AI-harness R10 bed driver (bed_run.go's runCheckBed) now calls bedSetup/bedTeardown
// (bed_session.go) directly — plain in-process function calls, no wire round-trip.

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
// keep_check_runs after a run. This plugin (like plugin-clean's own CLI) resolves the
// defaults.keep_check_runs PLUGIN-SIDE via the shared
// sdk/loaderkit.ResolveRetentionDefaultsViaExecutor (K-wave 2 cone R6 — the former
// "retention-defaults" HostBuild seam is DELETED), then reaches candy/plugin-clean's
// verb:retention over the PLUGIN↔PLUGIN InvokeProvider peer-dispatch leg (F10) with the
// resolved count filled in. The plugin prints the "Pruned N (keep_check_runs=K)" line from
// reply.CheckPaths/KeepCheckRuns.
func hostRetention(ex *sdk.Executor, ctx context.Context, req spec.RetentionRequest) (spec.RetentionReply, error) {
	req.KeepImages, req.KeepCheckRuns = loaderkit.ResolveRetentionDefaultsViaExecutor(ctx, ex, req.Dir)

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
