package bundle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// command.go is the command:bundle leg — the `charly bundle …` CLI, COMPILED-IN (F8). It dispatches
// IN-PROC via Invoke(OpRun): the reverse-channel executor is stashed (setCommandContext) so the
// moved BundleCmd handlers reach their host seams (deploy-add / deploy-del / deploy-from-box /
// deploy-config), then the pass-through args are kong-parsed into the BundleCmd
// tree and run. Because in-proc dispatch runs in charly's OWN process, the handlers inherit charly's
// real stdin/stdout/stderr/TTY natively — which keeps `charly bundle add`'s interactive prompts and
// dry-run output working exactly as before. Mirrors candy/plugin-vm/command.go.

// Invoke dispatches the COMPILED-IN (in-proc) command:bundle ops: OpRun (the `charly bundle …`
// CLI pass-through), OpCompile (the K4-B deploy-compile slice — the host's
// compileNodePlans computes the per-node selection and Invokes OpCompile; runBundleCompile
// re-hydrates the resolved-project envelope + loops deploykit.BuildDeployPlan), and
// OpDeployDispatch (S3b — the ONE generic envelope every former UnifiedDeployTarget/
// LifecycleTarget method dispatches through, see deploy_target.go). The retired
// K4-C spike's OpDispatch relay (dispatchNode's root-level Add nesting a second HostBuild call)
// is superseded by the walk port (walk.go): the plugin now drives the WHOLE tree walk itself and
// calls the deploy-tree-resolve / deploy-node-dispatch / deploy-members-* / deploy-del-resolve /
// deploy-node-del-dispatch seams directly, no OpDispatch round-trip needed.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetOp() {
	case sdk.OpRun:
		return runBundleCommand(ctx, req)
	case sdk.OpCompile:
		return runBundleCompile(ctx, req)
	case sdk.OpEphemeralRegister:
		return runEphemeralRegister(ctx, req)
	case sdk.OpEphemeralTeardown:
		return runEphemeralTeardown(ctx, req)
	case sdk.OpDeployDispatch:
		return runDeployDispatch(ctx, req)
	default:
		return nil, fmt.Errorf("bundle: unsupported op %q", req.GetOp())
	}
}

// runEphemeralRegister serves command:bundle's Invoke(OpEphemeralRegister): decode the
// #EphemeralRegisterRequest and register the ephemeral instance (FINAL/K5 unit 6a — the
// ephemeral_lifecycle.go move). Stashes the reverse-channel executor via setCommandContext
// (mirroring runBundleCompile) so persistEphemeralRuntime's saveDeployConfig call can reach the
// "deploy-config-save" HostBuild seam.
func runEphemeralRegister(ctx context.Context, req *pb.InvokeRequest) (reply *pb.InvokeReply, retErr error) {
	defer recoverEphemeralOpPanic(&retErr)
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("bundle ephemeral-register: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	var r spec.EphemeralRegisterRequest
	if err := json.Unmarshal(req.GetParamsJson(), &r); err != nil {
		return nil, fmt.Errorf("bundle ephemeral-register: decode request: %w", err)
	}
	if _, err := registerEphemeral(r.Node, r.Name); err != nil {
		return nil, fmt.Errorf("bundle ephemeral-register: %w", err)
	}
	replyJSON, err := json.Marshal(spec.EphemeralRegisterReply{})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: replyJSON}, nil
}

// runEphemeralTeardown serves command:bundle's Invoke(OpEphemeralTeardown): decode the
// #EphemeralTeardownRequest and tear down the ephemeral instance.
func runEphemeralTeardown(ctx context.Context, req *pb.InvokeRequest) (reply *pb.InvokeReply, retErr error) {
	defer recoverEphemeralOpPanic(&retErr)
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("bundle ephemeral-teardown: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	var r spec.EphemeralTeardownRequest
	if err := json.Unmarshal(req.GetParamsJson(), &r); err != nil {
		return nil, fmt.Errorf("bundle ephemeral-teardown: decode request: %w", err)
	}
	if err := teardownEphemeral(r.Node, r.Name); err != nil {
		return nil, fmt.Errorf("bundle ephemeral-teardown: %w", err)
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
// UNRECOVERED anywhere in the call chain and never surfaced — the enclosing `charly bundle add`
// reported PASS regardless. Placed at the OUTERMOST plugin-side entry point (runEphemeralRegister/
// runEphemeralTeardown) so it catches a panic from ANYWHERE inside registerEphemeral/
// teardownEphemeral, not just the one bug already found — a general safety net for this whole op
// class, matching the "silent failure must become loud" pattern this cutover keeps finding.
func recoverEphemeralOpPanic(errOut *error) {
	if r := recover(); r != nil {
		*errOut = fmt.Errorf("%s %v", sdk.EphemeralPanicMarker, r)
	}
}

// runBundleCommand serves command:bundle's Invoke(OpRun): recover the executor, decode the
// pass-through args, and run the BundleCmd tree (the plugin-vm command-dispatch pattern).
func runBundleCommand(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("bundle command: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("bundle command: decode args: %w", err)
		}
	}
	if rerr := dispatchBundleCLI(in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}

// dispatchBundleCLI kong-parses the pass-through args into the BundleCmd tree and runs the selected
// leaf.
func dispatchBundleCLI(args []string) error {
	var cli BundleCmd
	return sdk.RunInProcCLI("bundle", &cli, args)
}

// CliMain is the OUT-OF-PROCESS command entry — unreachable in the canonical compiled-in placement.
// command:bundle's handlers reach the host reverse channel (the deploy-add/del/from-box dispatch +
// the deploy-config seam), which is unavailable out-of-process, so this errors (like plugin-vm's CliMain).
func CliMain(_ []string) int {
	fmt.Fprintln(os.Stderr, "charly bundle: requires compiled-in placement (the command's host reverse channel is unavailable out-of-process)")
	return 1
}
