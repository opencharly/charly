package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// deploy_dispatch_seam.go — the K4-C host-side deploy-DISPATCH seam. The ONE new seam this wave
// adds: the boundary-law scoping pass found ResolveTarget(node,name) → UnifiedDeployTarget.Add —
// a live provider-registry lookup + a live Executor composition — is the SOLE genuinely-
// irreducible piece of the deploy kernel; everything around it either already moved (the
// InstallPlan compile loop, K4-B/OpCompile) or was a stale "stays core" claim. dispatchViaSeam
// routes EVERY Add dispatch (root AND nested) through command:bundle's OpDispatch leg — a
// PLUGIN-INITIATED call that nests a SECOND out-of-process substrate dispatch through
// HostBuild("deploy-dispatch") — proven live on both a local and a vm substrate (spike #1). A
// nested node's opts.ParentExec is encoded into a spec.VenueDescriptor (venueDescriptorForExecutor,
// deploy_venue_descriptor.go) and re-materialized host-side inside the seam handler — the SAME
// decouple point substrateLifecycle's PrepareVenue already uses for a root venue, generalized to
// a nested tree hop, so a live executor never actually crosses the wire.

// dispatchViaSeam marshals the resolved node + compiled plans + the EmitOpts scalar gates (never
// the whole deploykit.EmitOpts struct — ParentExec/ParentNode are handled separately, see above)
// into a spec.DeployDispatchRequest and Invokes command:bundle's OpDispatch. The plugin relays the
// request VERBATIM to HostBuild("deploy-dispatch") (hostBuildDeployDispatchAdd,
// host_build_deploy_dispatch.go), which reconstructs the config + DeployContext + the decoded
// parent executor and runs the actual ResolveTarget → UnifiedDeployTarget.Add.
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

	// K4-C venue-descriptor generalization: encode a NESTED node's parent executor (never nil
	// for a node reached via WalkDeploymentTree's callback under a live parent) so the seam
	// handler can re-materialize it before calling Add.
	if opts.ParentExec != nil {
		pd, err := venueDescriptorForExecutor(opts.ParentExec)
		if err != nil {
			return fmt.Errorf("dispatch: encode parent venue: %w", err)
		}
		if pd != nil {
			pdJSON, err := json.Marshal(pd)
			if err != nil {
				return fmt.Errorf("dispatch: marshal parent venue: %w", err)
			}
			req.ParentVenueJSON = pdJSON
		}
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
