package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// deploy_dispatch_seam.go — the K4-C host-side deploy-DISPATCH seam (P13-KERNEL spike #1). The
// ONE new seam this wave adds: the boundary-law scoping pass found ResolveTarget(node,name) →
// UnifiedDeployTarget.Add — a live provider-registry lookup + a live Executor composition — is
// the SOLE genuinely-irreducible piece of the deploy kernel; everything around it either already
// moved (the InstallPlan compile loop, K4-B/OpCompile) or was a stale "stays core" claim. This
// spike routes the root-level (non-nested) Add dispatch through command:bundle's new OpDispatch
// leg — a PLUGIN-INITIATED call that nests a SECOND out-of-process substrate dispatch through
// HostBuild("deploy-dispatch") — to prove the reverse-channel broker threads correctly when the
// outer call originates from the plugin (unlike OpCompile's HostBuild("resolved-project") call,
// which is host-initiated). dispatchViaSeam is the ONLY call site (bundle_add_cmd.go's
// dispatchNode, root-level only); a nested node keeps the direct in-process ResolveTarget().Add()
// call until a later increment threads a parent executor across the wire too.

// dispatchViaSeam marshals the resolved node + compiled plans + the EmitOpts scalar gates (never
// the whole deploykit.EmitOpts struct, which carries the live, non-marshalable
// ParentExec/ParentNode) into a spec.DeployDispatchRequest and Invokes command:bundle's
// OpDispatch. The plugin relays the request VERBATIM to HostBuild("deploy-dispatch")
// (hostBuildDeployDispatch, deploy_dispatch_host.go), which reconstructs the config +
// DeployContext and runs the actual ResolveTarget → UnifiedDeployTarget.Add.
func (c *deployAddCmd) dispatchViaSeam(node *spec.BundleNode, deployName, dir, base string, plans []*deploykit.InstallPlan, opts deploykit.EmitOpts) error {
	prov, ok := providerRegistry.resolve(ClassCommand, "bundle")
	if !ok {
		return fmt.Errorf("dispatch: command:bundle provider not loaded (candy/plugin-bundle must be compiled in via compiled_plugins:)")
	}

	views := make([]spec.InstallPlanView, 0, len(plans))
	for _, p := range plans {
		views = append(views, deploykit.WireView(p))
	}
	plansJSON, err := json.Marshal(views)
	if err != nil {
		return fmt.Errorf("dispatch: marshal plans: %w", err)
	}

	req := spec.DeployDispatchRequest{
		Dir:                  dir,
		Node:                 node,
		DeployName:           deployName,
		Op:                   "add",
		Target:               node.Target,
		Base:                 base,
		PlansJSON:            plansJSON,
		NodeOnly:             c.NodeOnly,
		DryRun:               opts.DryRun,
		FormatJSON:           opts.FormatJSON,
		AllowRepoChanges:     opts.AllowRepoChanges,
		AllowRootTasks:       opts.AllowRootTasks,
		WithServices:         opts.WithServices,
		SkipIncompatible:     opts.SkipIncompatible,
		AssumeYes:            opts.AssumeYes,
		Verify:               opts.Verify,
		Pull:                 opts.Pull,
		BuilderImageOverride: opts.BuilderImageOverride,
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("dispatch: marshal request: %w", err)
	}

	ctx := sdk.ContextWithExecutor(context.Background(),
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	_, err = prov.Invoke(ctx, &Operation{Reserved: "bundle", Op: sdk.OpDispatch, Params: reqJSON})
	return err
}

// dispatchDelViaSeam is deployDelCmd's counterpart of dispatchViaSeam: it marshals the resolved
// node + the adapter-level teardown gates + DelOpts into a spec.DeployDispatchRequest (op "del")
// and Invokes command:bundle's OpDispatch, which relays to HostBuild("deploy-dispatch") →
// hostBuildDeployDispatchDel. Unlike Add, Del has no nested-executor concept, so every del
// dispatch — not just the root-level one — routes through this seam.
func (c *deployDelCmd) dispatchDelViaSeam(node *spec.BundleNode, deployName string) error {
	prov, ok := providerRegistry.resolve(ClassCommand, "bundle")
	if !ok {
		return fmt.Errorf("dispatch: command:bundle provider not loaded (candy/plugin-bundle must be compiled in via compiled_plugins:)")
	}

	req := spec.DeployDispatchRequest{
		Node:            node,
		DeployName:      deployName,
		Op:              "del",
		Target:          node.Target,
		DryRun:          c.DryRun,
		AssumeYes:       c.AssumeYes,
		KeepRepoChanges: c.KeepRepoChanges,
		KeepServices:    c.KeepServices,
		KeepImage:       c.KeepImage,
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("dispatch: marshal request: %w", err)
	}

	ctx := sdk.ContextWithExecutor(context.Background(),
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	_, err = prov.Invoke(ctx, &Operation{Reserved: "bundle", Op: sdk.OpDispatch, Params: reqJSON})
	return err
}
