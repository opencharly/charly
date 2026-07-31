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
// TWO drive shapes, one per host caller (see spec.VerifyChecksRequest):
//   - Ops  → the deploy-lifecycle Test path (unified_targets.go runUnifiedTargetChecks): raw
//     deploy-scope checks via kit.Runner.Run (no plan gating), each verdict wrapped as a StepResult.
//   - Plan → the `target: local` --verify path (check_cmd.go runLocalDeployScopePlan): the host
//     ASSEMBLES the plan (kind:local template + node + per-host overlay — the deploy/K4 named-exit
//     assembly STAYS core) and this handler DRIVES it via kit.RunPlan, rebuilding the runtime env +
//     ${HOST:} host-vars + the cross-deployment TargetResolver from {dir, box, instance} exactly as
//     the check-live gather does (live_gather.go's pluginRunLocalDeployScopePlan).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
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

// verifyChecksRunOps drives the deploy-lifecycle Test path (raw Op checks, no plan gating) — the
// plugin-side port of the former core runUnifiedTargetChecks kit.Runner.Run drive. Each CheckResult
// is wrapped in a StepResult so the reply has one uniform shape; the host reads .Result.Status /
// .Result.Op.ID.
func verifyChecksRunOps(ex *sdk.Executor, ctx context.Context, venueExec spec.DeployExecutor, in spec.VerifyChecksRequest) []kit.StepResult {
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      in.Mode,
		VenueKind: venueExec.Kind(),
	}, kit.RunnerConfig{
		Exec: venueExec,
		Mode: verifyChecksMode(in.Mode),
	})
	crs := runner.Run(ctx, in.Ops)
	out := make([]kit.StepResult, 0, len(crs))
	for i := range crs {
		out = append(out, kit.StepResult{Result: crs[i]})
	}
	return out
}

// verifyChecksRunPlan drives the `target: local` --verify path — the plugin-side port of the former
// core runLocalDeployScopePlan RunPlan drive (byte-mirroring live_gather.go's
// pluginRunLocalDeployScopePlan: host-context env via venueExec.ResolveHome, ${HOST:} host-vars, the
// cross-deployment TargetResolver — all rebuilt from {dir, box, instance}, none crossing the wire).
func verifyChecksRunPlan(ex *sdk.Executor, ctx context.Context, venueExec spec.DeployExecutor, in spec.VerifyChecksRequest) []kit.StepResult {
	user := os.Getenv("USER")
	home, herr := venueExec.ResolveHome(ctx, user)
	if herr != nil || home == "" {
		home = os.Getenv("HOME")
	}
	resolver := kit.NewRuntimeCheckVarResolver(map[string]string{
		"IMAGE":    in.Box,
		"INSTANCE": in.Instance,
		"USER":     user,
		"HOME":     home,
	})
	env, hasRuntime := pluginResolverEnv(resolver)
	hostVars, hostCleanups := resolveHostVarsForSteps(ex, ctx, in.Dir, in.Plan, in.Instance)
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
	set := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{Origin: "local:" + in.Box, Plan: in.Plan}}}
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
