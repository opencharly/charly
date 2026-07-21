package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// ephemeral_dispatch.go — the host-side dispatch for command:bundle's OpEphemeralRegister/
// OpEphemeralTeardown legs (FINAL/K5 unit 6a): ephemeral_lifecycle.go's cross-substrate
// registration/teardown BODY moved to candy/plugin-bundle (the substrate-neutral deploy-lifecycle
// owner — the plugin body is substrate-agnostic, reached identically by any caller). Only
// vm_lifecycle_preresolve.go actually calls the register/teardown path TODAY (via
// deploy_add_shared.go's registerEphemeralIfMarked) — pod/k8s never reach it (verified by
// call-graph; the deleted charly/ephemeral_lifecycle.go's header falsely claimed all three did,
// an R1 instance this comment does not repeat). Wiring pod/k8s is tracked to the bed-robustness
// batch; `ephemeral: true` on a pod/k8s deploy is rejected at load time in the meantime
// (validate_ephemeral.go), never silently inert. deploy_add_shared.go's registerEphemeralIfMarked
// and vm_lifecycle_preresolve.go's vmLifecyclePostTeardown STAY host-side (candidate-floor
// siblings of bundle_add_cmd.go, pending FLOOR-SLIM adjudication) and reach the plugin here,
// mirroring bundle_compile_seam.go's compileViaPlugin host→plugin dispatch shape exactly:
// plugin-bundle is COMPILED-IN, so providerRegistry.resolve is a direct, always-registered
// lookup — no InvokeProvider lazy-connect needed (safe today; also robust once unit 6b's
// InvokeProvider generalization lands, since this dispatch already goes through the registry).

// RegisterEphemeralLifecycle dispatches command:bundle's OpEphemeralRegister. Registration
// failure is logged plugin-side (best-effort, matching the prior in-core contract) — the plugin
// returns success unless the WHOLE registration cannot proceed, so a caller here only needs the
// error (the prior *EphemeralHandle return value was already discarded by its one caller,
// registerEphemeralIfMarked — dropped from the signature, not silently ignored).
func RegisterEphemeralLifecycle(node *spec.BundleNode, deployName string) error {
	return dispatchEphemeralOp(sdk.OpEphemeralRegister, node, deployName)
}

// TeardownEphemeralLifecycle dispatches command:bundle's OpEphemeralTeardown.
func TeardownEphemeralLifecycle(node *spec.BundleNode, deployName string) error {
	return dispatchEphemeralOp(sdk.OpEphemeralTeardown, node, deployName)
}

func dispatchEphemeralOp(op string, node *spec.BundleNode, deployName string) error {
	prov, ok := providerRegistry.resolve(ClassCommand, "bundle")
	if !ok {
		return fmt.Errorf("ephemeral %s: command:bundle provider not loaded (candy/plugin-bundle must be compiled in via compiled_plugins:)", op)
	}
	var reqJSON []byte
	var err error
	switch op {
	case sdk.OpEphemeralRegister:
		reqJSON, err = json.Marshal(spec.EphemeralRegisterRequest{Name: deployName, Node: node})
	case sdk.OpEphemeralTeardown:
		reqJSON, err = json.Marshal(spec.EphemeralTeardownRequest{Name: deployName, Node: node})
	default:
		return fmt.Errorf("ephemeral dispatch: unknown op %q", op)
	}
	if err != nil {
		return fmt.Errorf("ephemeral %s: marshal request: %w", op, err)
	}
	// command:bundle is compiled-in (in-proc); the reverse server carries no venue executor —
	// OpEphemeralRegister/Teardown need only the "deploy-config-save" host-build seam, exactly
	// like dispatchBuild's / compileViaPlugin's in-proc reverse channel.
	ctx := sdk.ContextWithExecutor(context.Background(),
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	if _, err := prov.Invoke(ctx, &Operation{Reserved: "bundle", Op: op, Params: reqJSON}); err != nil {
		return fmt.Errorf("ephemeral %s: bundle plugin: %w", op, err)
	}
	return nil
}
