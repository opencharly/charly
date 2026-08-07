package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// command.go is the command:fleet leg — the `charly fleet …` CLI, COMPILED-IN (F8). It dispatches
// IN-PROC via Invoke(OpRun): the reverse-channel executor is stashed (setCommandContext) so the
// moved FleetCmd handlers reach their host seams (deploy-add / deploy-del / deploy-config —
// from-box is fully plugin-side since K-wave 2 cone R2), then the pass-through args are
// kong-parsed into the FleetCmd
// tree and run. Because in-proc dispatch runs in charly's OWN process, the handlers inherit charly's
// real stdin/stdout/stderr/TTY natively — which keeps `charly fleet add`'s interactive prompts and
// dry-run output working exactly as before. Mirrors candy/plugin-vm/command.go.

// Invoke dispatches the COMPILED-IN (in-proc) command:fleet ops: OpRun (the `charly fleet …`
// CLI pass-through), OpCompile (the K4-B deploy-compile slice — runFleetCompile re-hydrates the
// resolved-project envelope + loops deploykit.BuildDeployPlan via the shared compilePlansForRequest;
// after K4-C shape-2 the plugin's OWN walk calls that shared fn IN-PROC (dispatch.go compileNodePlans)
// with no OpCompile round-trip, and OpCompile stays as the wire leg the parity test exercises), and
// OpDeployDispatch (S3b — the ONE generic envelope every former UnifiedDeployTarget/LifecycleTarget
// method dispatches through, see deploy_target.go). The plugin drives the WHOLE tree walk itself
// (walk.go) and calls the deploy-plugins-connect / resolve-target-add / deploy-members-* /
// deploy-node-del-dispatch seams directly (the del RESOLUTION runs plugin-side, del_resolve.go —
// the deploy-del-resolve seam is DELETED, K-wave 2 cone R2 bank C).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetOp() {
	case sdk.OpRun:
		return runFleetCommand(ctx, req)
	case sdk.OpCompile:
		return runFleetCompile(ctx, req)
	case sdk.OpEphemeralRegister:
		return runEphemeralRegister(ctx, req)
	case sdk.OpEphemeralTeardown:
		return runEphemeralTeardown(ctx, req)
	case sdk.OpDeployDispatch:
		return runDeployDispatch(ctx, req)
	default:
		return nil, fmt.Errorf("fleet: unsupported op %q", req.GetOp())
	}
}

// runEphemeralRegister serves command:fleet's Invoke(OpEphemeralRegister): decode the
// #EphemeralRegisterRequest and register the ephemeral instance (FINAL/K5 unit 6a — the
// ephemeral_lifecycle.go move). Stashes the reverse-channel executor via setCommandContext
// (mirroring runFleetCompile) so persistEphemeralRuntime's saveDeployConfig call can reach the
// reverse channel — the loaderkit.LoadHostFleetConfigViaExecutor overlay read + the "loader-threaded" Primaries snapshot
// its PLUGIN-SIDE deploykit.SaveFleetConfig write needs (#55 K4 — no host deploy-config-save seam).
func runEphemeralRegister(ctx context.Context, req *pb.InvokeRequest) (reply *pb.InvokeReply, retErr error) {
	defer recoverEphemeralOpPanic(&retErr)
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("fleet ephemeral-register: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	var r spec.EphemeralRegisterRequest
	if err := json.Unmarshal(req.GetParamsJson(), &r); err != nil {
		return nil, fmt.Errorf("fleet ephemeral-register: decode request: %w", err)
	}
	if _, err := registerEphemeral(r.Node, r.Name); err != nil {
		return nil, fmt.Errorf("fleet ephemeral-register: %w", err)
	}
	replyJSON, err := json.Marshal(spec.EphemeralRegisterReply{})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: replyJSON}, nil
}

// runEphemeralTeardown serves command:fleet's Invoke(OpEphemeralTeardown): decode the
// #EphemeralTeardownRequest and tear down the ephemeral instance.
func runEphemeralTeardown(ctx context.Context, req *pb.InvokeRequest) (reply *pb.InvokeReply, retErr error) {
	defer recoverEphemeralOpPanic(&retErr)
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("fleet ephemeral-teardown: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	var r spec.EphemeralTeardownRequest
	if err := json.Unmarshal(req.GetParamsJson(), &r); err != nil {
		return nil, fmt.Errorf("fleet ephemeral-teardown: decode request: %w", err)
	}
	if err := teardownEphemeral(r.Node, r.Name); err != nil {
		return nil, fmt.Errorf("fleet ephemeral-teardown: %w", err)
	}
	replyJSON, err := json.Marshal(spec.EphemeralTeardownReply{})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: replyJSON}, nil
}

// recoverEphemeralOpPanic converts a recovered panic into an error carrying sdk.EphemeralPanicMarker,
// assigning it to *errOut (the caller's named error return) instead of letting it crash or vanish.
// RCA #5 (FINAL/K5 unit 6a): persistEphemeralRuntime's nil-map write panic was previously
// UNRECOVERED anywhere in the call chain and never surfaced — the enclosing `charly fleet add`
// reported PASS regardless. Placed at the OUTERMOST plugin-side entry point (runEphemeralRegister/
// runEphemeralTeardown) so it catches a panic from ANYWHERE inside registerEphemeral/
// teardownEphemeral, not just the one bug already found — a general safety net for this whole op
// class, matching the "silent failure must become loud" pattern this cutover keeps finding.
func recoverEphemeralOpPanic(errOut *error) {
	if r := recover(); r != nil {
		*errOut = fmt.Errorf("%s %v", sdk.EphemeralPanicMarker, r)
	}
}

// runFleetCommand serves command:fleet's Invoke(OpRun): recover the executor, decode the
// pass-through args, and run the FleetCmd tree (the plugin-vm command-dispatch pattern).
func runFleetCommand(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("fleet command: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	var in struct {
		Args        []string        `json:"args"`
		HostEnvJSON json.RawMessage `json:"host_env_json"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("fleet command: decode args: %w", err)
		}
	}
	// Stash the host-side spec.HostEnv threaded as DATA on the OpRun dispatch (core computes it —
	// os.Executable() is only correct in-core, R10 bed-found bug #5). The from-box pod path reads
	// it to populate PodConfigSetupRequest.HostEnvJSON (deploy:pod's encrypted-mount ExecStartPre
	// CharlyBin line).
	cmdHostEnvJSON = in.HostEnvJSON
	if rerr := dispatchFleetCLI(in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}

// dispatchFleetCLI kong-parses the pass-through args into the FleetCmd tree and runs the selected
// leaf.
func dispatchFleetCLI(args []string) error {
	var cli FleetCmd
	return sdk.RunInProcCLI("fleet", &cli, args)
}

// CliMain is the OUT-OF-PROCESS command entry — unreachable in the canonical compiled-in placement.
// command:fleet's handlers reach the host reverse channel (the deploy-add/del/from-box dispatch +
// the deploy-config seam), which is unavailable out-of-process, so this errors (like plugin-vm's CliMain).
func CliMain(_ []string) int {
	fmt.Fprintln(os.Stderr, "charly fleet: requires compiled-in placement (the command's host reverse channel is unavailable out-of-process)")
	return 1
}
