package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// ephemeral_dispatch.go — the host-side dispatch for command:bundle's OpEphemeralRegister leg
// (FINAL/K5 unit 6a): ephemeral_lifecycle.go's cross-substrate registration BODY moved to
// candy/plugin-bundle (the substrate-neutral deploy-lifecycle owner — the plugin body is
// substrate-agnostic, reached identically by any caller). Only deploy_add_shared.go's
// registerEphemeralIfMarked calls this TODAY; pod/k8s never reach it (verified by call-graph; the
// deleted charly/ephemeral_lifecycle.go's header falsely claimed all three did, an R1 instance
// this comment does not repeat). Wiring pod/k8s is tracked to the bed-robustness batch;
// `ephemeral: true` on a pod/k8s deploy is rejected at load time in the meantime
// (validate_ephemeral.go), never silently inert. deploy_add_shared.go's registerEphemeralIfMarked
// STAYS host-side (a candidate-floor sibling of bundle_add_cmd.go, pending FLOOR-SLIM
// adjudication) and reaches the plugin here, mirroring deploy_target_dispatch.go's
// dispatchDeployTarget host→plugin dispatch shape exactly: plugin-bundle is COMPILED-IN, so
// providerRegistry.resolve is a direct, always-registered lookup — no InvokeProvider lazy-connect
// needed (safe today; also robust once unit 6b's InvokeProvider generalization lands, since this
// dispatch already goes through the registry).
//
// The OpEphemeralTeardown twin (TeardownEphemeralLifecycle) that used to live here is DELETED (F6
// vm-lifecycle move, coneB-vmlifecycle): its sole caller, vm_lifecycle_preresolve.go's
// vmLifecyclePostTeardown, moved plugin-side (candy/plugin-deploy-vm/lifecycle.go's
// vmPostTeardown), which reaches command:bundle's OpEphemeralTeardown directly via
// exec.InvokeProvider — no core dispatch needed for an out-of-process caller.

// RegisterEphemeralLifecycle dispatches command:bundle's OpEphemeralRegister. Registration
// failure is logged plugin-side (best-effort, matching the prior in-core contract) — the plugin
// returns success unless the WHOLE registration cannot proceed, so a caller here only needs the
// error (the prior *EphemeralHandle return value was already discarded by its one caller,
// registerEphemeralIfMarked — dropped from the signature, not silently ignored).
func RegisterEphemeralLifecycle(node *spec.BundleNode, deployName string) error {
	prov, ok := providerRegistry.resolve(ClassCommand, "bundle")
	if !ok {
		return fmt.Errorf("ephemeral %s: command:bundle provider not loaded (candy/plugin-bundle must be compiled in via compiled_plugins:)", sdk.OpEphemeralRegister)
	}
	reqJSON, err := json.Marshal(spec.EphemeralRegisterRequest{Name: deployName, Node: node})
	if err != nil {
		return fmt.Errorf("ephemeral %s: marshal request: %w", sdk.OpEphemeralRegister, err)
	}
	// command:bundle is compiled-in (in-proc); the reverse server carries no venue executor —
	// OpEphemeralRegister needs only the "deploy-config-save" host-build seam, exactly like
	// dispatchBuild's / dispatchDeployTarget's in-proc reverse channel.
	ctx := sdk.ContextWithExecutor(context.Background(),
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	if _, err := prov.Invoke(ctx, &Operation{Reserved: "bundle", Op: sdk.OpEphemeralRegister, Params: reqJSON}); err != nil {
		return fmt.Errorf("ephemeral %s: bundle plugin: %w", sdk.OpEphemeralRegister, err)
	}
	return nil
}
