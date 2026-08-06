package main

import (
	"context"
	"encoding/json"
	"fmt"

	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// deploy_target_dispatch.go — the core-side half of the S3b move: dispatchDeployTarget invokes
// command:bundle's OpDeployDispatch (the ONE generic envelope every UnifiedDeployTarget/
// LifecycleTarget method now dispatches through) WITH a live executor, mirroring
// ephemeral_dispatch.go's dispatchEphemeralOp — the
// SAME "compiled-in in-proc reverse channel" pattern arbiterInvoke (folded from the deleted
// preempt.go into host_build_pod_lifecycle_dispatch.go) established:
// thread the executor via specexec.ContextWithExecutor(ctx, specexec.NewInProcExecutor(&inprocExecutorClient
// {srv: &executorReverseServer{...}})) before calling prov.Invoke — no broker needed, since
// command:bundle is COMPILED-IN (an inprocProvider). The plugin's own OpDeployDispatch handler
// recovers this SAME executor via specexec.ExecutorForInvoke(ctx, brokerID) (ctx-first) and threads it
// onward to the ACTUAL substrate provider via its own specexec.Executor.InvokeProvider (S1) — core
// never touches the substrate's *grpcProvider directly once this call returns.
func dispatchDeployTarget(ctx context.Context, req spec.DeployTargetDispatchRequest, exec spec.DeployExecutor, build buildEngineContext, rebootable bool) (spec.DeployTargetDispatchReply, error) {
	prov, ok := providerRegistry.resolve(ClassCommand, "bundle")
	if !ok {
		return spec.DeployTargetDispatchReply{}, fmt.Errorf("deploy-dispatch %s: command:bundle provider not loaded (candy/plugin-bundle must be compiled in via compiled_plugins:)", req.Op)
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return spec.DeployTargetDispatchReply{}, fmt.Errorf("deploy-dispatch %s: marshal request: %w", req.Op, err)
	}
	invokeCtx := specexec.ContextWithExecutor(ctx,
		specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{exec: exec, build: build, rebootable: rebootable}}))
	res, err := prov.Invoke(invokeCtx, &Operation{Reserved: "bundle", Op: ops.OpDeployDispatch, Params: reqJSON})
	if err != nil {
		return spec.DeployTargetDispatchReply{}, fmt.Errorf("deploy-dispatch %s: bundle plugin: %w", req.Op, err)
	}
	var reply spec.DeployTargetDispatchReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return spec.DeployTargetDispatchReply{}, fmt.Errorf("deploy-dispatch %s: decode reply: %w", req.Op, err)
		}
	}
	return reply, nil
}
