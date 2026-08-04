package check

// verify_checks.go — the command:check DEPLOY-VERIFY drive (OpVerifyChecks, #55 CHECK-ENGINE cone
// Unit 2). The host (charly core) holds a live venue executor for a deploy-scope check pass but no
// longer builds the in-proc kit.Runner itself — checkrun.go's newCheckRunner + carrierFromRunner +
// resolverEnv and planrun_adapter.go's venueResolver were the ONLY thing forcing core's checkrun.go
// + planrun_adapter.go to import sdk/kit. This handler moves that DRIVE plugin-side so both files
// shed their kit import (24→22 charly kit importers).
//
// The host flattens the live executor to a spec.VenueDescriptor (a live executor cannot cross the
// wire) and threads it in the request; this handler re-materializes it via kit.VenueFromDescriptor —
// the SAME mechanism candy/plugin-bundle's resolveRootExecutor uses. Because command:check is
// COMPILED-IN, this runs in charly's own process, so a re-materialized shell/ssh executor is
// byte-identical to the one the host held. The reverse-channel *sdk.Executor (recovered via
// ExecutorForInvoke) is used only for the plugin's OWN legs — the pluginVerbResolver's InvokeProvider
// verb dispatch and the local-verify path's resolvedProject/TargetResolver HostBuild.
//
// TWO drive shapes, one per caller (see spec.VerifyChecksRequest):
//   - Ops  → the deploy-lifecycle Test path (charly core's unified_targets.go runUnifiedTargetChecks):
//     raw deploy-scope checks via kit.Runner.Run (no plan gating), each verdict wrapped as a StepResult.
//   - Plan → the `target: local` --verify path — candy/plugin-bundle's verify_local.go
//     (verifyLocalDeployScope/localDeployScopePlan, #55 W3 B3, relocated from the former core
//     check_cmd.go's runLocalDeployScopePlan) ASSEMBLES the plan (kind:local template, resolved
//     PLUGIN-SIDE via node_resolve.go's lookupLocalTemplate — no LoadUnified needed — + the deploy
//     node's own plan) and this handler DRIVES it via kit.RunPlan, rebuilding the runtime env +
//     ${HOST:} host-vars + the cross-deployment TargetResolver from {dir, box, instance} exactly as
//     the check-live gather does (live_gather.go's pluginRunLocalDeployScopePlan) — a PEER plugin
//     calling THIS plugin now, not core.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// verifyChecksForHost serves one command:check OpVerifyChecks: re-materialize the threaded venue,
// drive the requested check pass, and reply with the sanctioned sdk/kit []StepResult wire.
func verifyChecksForHost(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var in spec.VerifyChecksRequest
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-check: decode verify-checks request: %w", err)
		}
	}
	ex, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-check: verify-checks reverse-channel executor: %w", err)
	}
	venueExec, err := kit.VenueFromDescriptor(in.Venue)
	if err != nil {
		return nil, fmt.Errorf("plugin-check: verify-checks re-materialize venue: %w", err)
	}

	var results []kit.StepResult
	if len(in.Plan) > 0 {
		results = verifyChecksRunPlan(ex, ctx, venueExec, in)
	} else {
		results = verifyChecksRunOps(ex, ctx, venueExec, in)
	}

	out, err := json.Marshal(results)
	if err != nil {
		return nil, fmt.Errorf("plugin-check: marshal verify-checks reply: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

// filterOpsByID subsets ops to the listed IDs, preserving ops' order — the pure logic behind
// in.OnlyIDs (K-wave W3a A9: the former core-side pre-filter loop in
// charly/unified_targets.go's runUnifiedTargetChecks, moved here since candy/plugin-check is
// where the ops are about to run). Empty/nil onlyIDs is a no-op (returns ops unchanged).
func filterOpsByID(ops []spec.Op, onlyIDs []string) []spec.Op {
	if len(onlyIDs) == 0 {
		return ops
	}
	only := make(map[string]bool, len(onlyIDs))
	for _, id := range onlyIDs {
		only[id] = true
	}
	filtered := make([]spec.Op, 0, len(ops))
	for _, op := range ops {
		if only[op.ID] {
			filtered = append(filtered, op)
		}
	}
	return filtered
}

// verifyChecksRunOps drives the deploy-lifecycle Test path (raw Op checks, no plan gating) — the
// plugin-side port of the former core runUnifiedTargetChecks kit.Runner.Run drive. Each CheckResult
// is wrapped in a StepResult so the reply has one uniform shape; the host reads .Result.Status /
// .Result.Op.ID.
func verifyChecksRunOps(ex *sdk.Executor, ctx context.Context, venueExec spec.DeployExecutor, in spec.VerifyChecksRequest) []kit.StepResult {
	ops := filterOpsByID(in.Ops, in.OnlyIDs)
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      in.Mode,
		VenueKind: venueExec.Kind(),
	}, kit.RunnerConfig{
		Exec: venueExec,
		Mode: verifyChecksMode(in.Mode),
	})
	crs := runner.Run(ctx, ops)
	out := make([]kit.StepResult, 0, len(crs))
	for i := range crs {
		// crs[i] is the engine kit.CheckResult (with the DeadlineExceeded flag the retry loop
		// reads); the StepResult wire field carries the embedded spec.CheckResult only
		// (DeadlineExceeded is engine-internal, json:"-"), so extract it here.
		out = append(out, kit.StepResult{Result: crs[i].CheckResult})
	}
	return out
}

// verifyChecksRunPlan drives the `target: local` --verify path — the plugin-side port of the former
// core runLocalDeployScopePlan RunPlan drive (byte-mirroring live_gather.go's
// pluginRunLocalDeployScopePlan: host-context env via venueExec.ResolveHome, ${HOST:} host-vars, the
// cross-deployment TargetResolver — all rebuilt from {dir, box, instance}, none crossing the wire).
//
// The per-host charly.yml OVERLAY merge (the deploy-entry `check:` extends/overrides) lives HERE
// now (#55 CHECK-ENGINE cone Option A — relocated from the former core runLocalDeployScopePlan so
// the `target: local` --verify path imports zero deploykit). The caller threads the base plan
// (kind:local template + deploy node, assembled PLUGIN-side now in candy/plugin-bundle's
// verify_local.go, #55 W3 B3); this handler appends the per-host overlay entry's plan — keyed by
// DeployKey(box, instance) with
// the bare-image fallback, the SAME precedence the core read used — before driving RunPlan, so the
// final plan run by the plugin is byte-identical to the former core-assembled one (base + overlay).
func verifyChecksRunPlan(ex *sdk.Executor, ctx context.Context, venueExec spec.DeployExecutor, in spec.VerifyChecksRequest) []kit.StepResult {
	user := os.Getenv("USER")
	home, herr := venueExec.ResolveHome(ctx, user)
	if herr != nil || home == "" {
		home = os.Getenv("HOME")
	}
	// Append the per-host overlay entry's plan (relocated from the former core
	// runLocalDeployScopePlan — see the header). DeployKey(box, instance) with the bare-image
	// fallback, the SAME precedence the core read used.
	plan := in.Plan
	if dc, derr := loaderkit.LoadHostBundleConfigViaExecutor(ctx, ex); derr == nil && dc != nil {
		if entry, ok := dc.Bundle[spec.DeployKey(in.Box, in.Instance)]; ok {
			plan = append(plan, entry.Plan...)
		} else if entry, ok := dc.Bundle[in.Box]; ok {
			plan = append(plan, entry.Plan...)
		}
	}
	resolver := kit.NewRuntimeCheckVarResolver(map[string]string{
		"IMAGE":    in.Box,
		"INSTANCE": in.Instance,
		"USER":     user,
		"HOME":     home,
	})
	env, hasRuntime := pluginResolverEnv(resolver)
	hostVars, hostCleanups := resolveHostVarsForSteps(ex, ctx, in.Dir, plan, in.Instance)
	defer kit.CloseHostCleanups(hostCleanups)

	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      in.Mode,
		Box:       in.Box,
		Instance:  in.Instance,
		VenueKind: venueExec.Kind(),
	}, kit.RunnerConfig{
		Exec:           venueExec,
		Mode:           verifyChecksMode(in.Mode),
		Env:            env,
		HasRuntime:     hasRuntime,
		Box:            in.Box,
		Instance:       in.Instance,
		VerifyOnly:     in.VerifyOnly,
		HostVars:       hostVars,
		TargetResolver: pluginVenueResolver(ex, ctx, in.Dir, in.Instance),
	})
	set := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{Origin: "local:" + in.Box, Plan: plan}}}
	return kit.RunPlan(ctx, runner, set, false)
}

// verifyChecksMode maps the wire mode string to the kit run mode (mirrors the "live"|"box" split;
// deploy-verify is always "live" today, defaulted here so an empty mode is not "box").
func verifyChecksMode(mode string) kit.RunMode {
	if mode == "box" {
		return kit.ModeBox
	}
	return kit.ModeLive
}
