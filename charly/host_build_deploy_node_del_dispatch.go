package main

import (
	"context"
	"fmt"

	"github.com/opencharly/spec/spec"
)

// host_build_deploy_node_del_dispatch.go — the "deploy-node-del-dispatch" F10 host-builder
// (K4-C walk port): ResolveTarget + target.Del, honoring the teardown gates. The plugin resolves
// the node (del_resolve.go) + strips the "vm:" addressing prefix before sending, and ships the
// target's ROOT-FIRST ancestor path/node lists so this half can reconstruct the ancestor executor
// chain (spec.ReconstructParentExec over the registry-coupled deriveChildExecutorForPath) exactly
// as the resolve-target-add half does; a live ReverseRunner is never carried on the wire.
const deployNodeDelDispatchBuilderKind = "deploy-node-del-dispatch"

func hostBuildDeployNodeDelDispatch(_ context.Context, req spec.DeployNodeDelDispatchRequest, _ buildEngineContext) (spec.DeployNodeDelDispatchReply, error) {
	return spec.DeployNodeDelDispatchReply{}, runDeployNodeDelDispatch(req)
}

// runDeployNodeDelDispatch is the `del` twin of runResolveTargetAdd (host_build_resolve_target_add.go):
// the same ancestor-chain reconstruction, applied to the teardown instead of the apply.
//
// The ADD half already hands Del's sibling `Add` the live parent executor via EmitOpts.ParentExec
// (applyParentExecOverride), which is what makes an in-substrate nested member deploy INTO its
// parent's venue. `Del` had no equivalent: the del dispatch carried no VenueJSON at all, so the
// plugin's resolveRootExecutor (candy/plugin-fleet/deploy_target.go) fell through to
// specexec.RootExecutorForDeployNode(req.Node) — the OPERATOR'S HOST for a member whose `host:`
// field is empty — and replayed the member's reversible ops (a `package:` list's `pacman -R`)
// against the workstation while its `add` had landed correctly in the guest. charly#765, the third
// variant of the #627/#680 mechanism; the check bed's cleanup step forks a FRESH
// `charly deploy del <root>.<member>` process (candy/plugin-check/bed_run.go), so the in-process
// venue carry between add and del never applied there either.
//
// The chain is re-derived HOST-side from the wire-safe lists, exactly as the add half does — a live
// DeployExecutor never crosses the wire. An empty list (a top-level or synthetic target: `host`,
// a bare name, a namespace-qualified root key) leaves the previous RootExecutorForDeployNode
// behaviour untouched, and a lifecycle substrate is skipped by applyDelParentExec just as the add
// half skips it (a vm/pod composes its OWN venue INSIDE PrepareVenue/teardown-executor).
func runDeployNodeDelDispatch(req spec.DeployNodeDelDispatchRequest) error {
	utgt, err := ResolveTarget(req.Node, req.Name)
	if err != nil {
		return fmt.Errorf("deploy-node-del-dispatch: resolve target: %w", err)
	}
	parentExec, err := spec.ReconstructParentExec(req.AncestorPaths, req.AncestorNodes, deriveChildExecutorForPath)
	if err != nil {
		return fmt.Errorf("deploy-node-del-dispatch: reconstruct ancestor chain: %w", err)
	}
	if tt, ok := utgt.(*pluginDeployTarget); ok {
		tt.applyDelParentExec(parentExec)
	}
	opts := spec.DeployTargetDelOpts{
		DryRun: req.DryRun, AssumeYes: req.AssumeYes,
		KeepRepoChanges: req.KeepRepoChanges, KeepServices: req.KeepServices, KeepImage: req.KeepImage,
	}
	if err := utgt.Del(context.Background(), opts); err != nil {
		return err
	}
	return nil
}

var _ = func() bool {
	registerHostBuilder(deployNodeDelDispatchBuilderKind, typedHostBuilder(deployNodeDelDispatchBuilderKind, hostBuildDeployNodeDelDispatch))
	return true
}()
