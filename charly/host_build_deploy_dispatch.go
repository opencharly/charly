package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_dispatch.go — the K4 lane A host-builders. The `charly bundle add` / `charly
// bundle del` dispatch CONTROL FLOW (Run's target-path resolve + the pre-order tree walk;
// deployDelCmd.Run's resolve-then-teardown sequence) moved OUT of core into candy/plugin-bundle
// (dispatch.go): the plugin now walks the tree itself (WalkDeploymentTree/ResolveNodePath are
// pure sdk/deploykit functions, already importable from either side) and drives each step via a
// narrow, generic HostBuild seam — RDD-spike-proven live (a real charly bundle __spike-add/del
// round-trip against check-preempt-local; see the K4 keystone spike report). What genuinely
// CANNOT cross the module boundary stays here: the provider registry (ResolveTarget), the
// concrete externalDeployTarget/grpcProvider handles, LoadUnified (the project loader), and the
// ledger. Six seams replace the OLD monolithic "deploy-add"/"deploy-del" (which reconstructed
// deployAddCmd{}/deployDelCmd{} and ran Run() verbatim, per host_build_deploy_add.go /
// host_build_deploy_del.go — now DELETED):
//
//   - "deploy-tree-resolve"      — resolveTreeRoot + loadDeployPlugins + the root's venue-ssh
//                                  trait (once, at the top of `charly bundle add`).
//   - "deploy-node-dispatch"     — dispatchNode (bundle_add_cmd.go, UNCHANGED), reached once per
//                                  tree position; ancestor_paths/ancestor_nodes let this rebuild
//                                  the SAME parentExec chain the plugin's OWN walk computed
//                                  (deriveChildExecutorForPath is pure — re-run HOST-side here
//                                  instead of receiving a live DeployExecutor over the wire).
//   - "deploy-members-bring-up"  — bringUpMembers (once, at the end of a non-dry-run add).
//   - "deploy-del-resolve"       — resolveDelNode (bundle_add_cmd.go, UNCHANGED); a pure read, no
//                                  ledger lock needed.
//   - "deploy-node-del-dispatch" — the ledger lock + loadDeployPlugins + ResolveTarget + the
//                                  teardown-gate flags + tearDownMembers + target.Del (the OLD
//                                  deployDelCmd.Run's tail, unchanged in substance).

const (
	deployTreeResolveBuilderKind     = "deploy-tree-resolve"
	deployNodeDispatchBuilderKind    = "deploy-node-dispatch"
	deployMembersBringUpBuilderKind  = "deploy-members-bring-up"
	deployDelResolveBuilderKind      = "deploy-del-resolve"
	deployNodeDelDispatchBuilderKind = "deploy-node-del-dispatch"
)

// hostBuildDeployTreeResolve resolves the merged project+operator deploy tree + connects the
// deployment's out-of-tree plugin candies, mirroring the FIRST two steps of the OLD
// deployAddCmd.Run (loadDeployPlugins, resolveTreeRoot) — reads LoadUnified, a core Mechanism the
// plugin cannot import. root_venue_ssh mirrors Run's `nodeTraits(rootNode).Venue == "ssh"` check
// via a HOST-side ResolveNodePath lookup, so the plugin doesn't need the registry-backed
// nodeTraits call itself.
func hostBuildDeployTreeResolve(_ context.Context, req spec.DeployTreeResolveRequest, _ buildEngineContext) (spec.DeployTreeResolveReply, error) {
	dir, err := getwdOrErr("deploy-tree-resolve")
	if err != nil {
		return spec.DeployTreeResolveReply{}, err
	}
	if err := loadDeployPlugins(dir, req.Path, req.AddCandy); err != nil {
		return spec.DeployTreeResolveReply{}, err
	}
	tree, err := resolveTreeRoot(dir)
	if err != nil {
		return spec.DeployTreeResolveReply{}, err
	}
	reply := spec.DeployTreeResolveReply{Tree: pointerizeTree(tree)}
	if tree != nil {
		if rootNode, _, nodeErr := deploykit.ResolveNodePath(tree, req.Path); nodeErr == nil && rootNode != nil {
			reply.RootVenueSSH = nodeTraits(rootNode).Venue == "ssh"
		}
	}
	return reply, nil
}

// pointerizeTree converts the value-typed tree map dispatch/deploykit helpers return into the
// pointer-valued map the wire type carries (spec.DeployTreeResolveReply.Tree is map[string]*Deploy
// — a def-having map field needs the explicit pointer pin, matching nested:/peer: in deploy.cue).
func pointerizeTree(tree map[string]spec.BundleNode) map[string]*spec.BundleNode {
	if tree == nil {
		return nil
	}
	out := make(map[string]*spec.BundleNode, len(tree))
	for k, v := range tree {
		n := v
		out[k] = &n
	}
	return out
}

// hostBuildDeployNodeDispatch wraps dispatchNode (bundle_add_cmd.go, UNCHANGED) as the per-node
// HostBuild seam: it reconstructs parentExec by folding deriveChildExecutorForPath over the
// plugin-computed ancestor chain (the SAME fold Run's ancestor loop used to perform in-process),
// builds the deployAddCmd from the flat CLI-flag fields, and calls dispatchNode exactly as Run
// did per tree position.
func hostBuildDeployNodeDispatch(_ context.Context, req spec.DeployNodeDispatchRequest, _ buildEngineContext) (spec.DeployNodeDispatchReply, error) {
	dir, err := getwdOrErr("deploy-node-dispatch")
	if err != nil {
		return spec.DeployNodeDispatchReply{}, err
	}

	var parentExec deploykit.DeployExecutor
	for i, anc := range req.AncestorNodes {
		a := anc
		next, derr := deriveChildExecutorForPath(req.AncestorPaths[i], &a, parentExec)
		if derr != nil {
			return spec.DeployNodeDispatchReply{}, fmt.Errorf("deploy-node-dispatch: deriving executor for ancestor %q: %w", req.AncestorPaths[i], derr)
		}
		parentExec = next
	}

	c := &deployAddCmd{
		Name:             req.Path,
		Ref:              req.Ref,
		AddCandy:         req.AddCandy,
		Tag:              req.Tag,
		DryRun:           req.DryRun,
		Format:           req.Format,
		Pull:             req.Pull,
		Verify:           req.Verify,
		WithServices:     req.WithServices,
		AllowRepoChanges: req.AllowRepoChanges,
		AllowRootTasks:   req.AllowRootTasks,
		SkipIncompatible: req.SkipIncompatible,
		BuilderImage:     req.BuilderImage,
		AssumeYes:        req.AssumeYes,
		Disposable:       req.Disposable,
		Lifecycle:        req.Lifecycle,
	}
	if c.Format == "" {
		c.Format = "table"
	}
	return spec.DeployNodeDispatchReply{}, c.dispatchNode(req.Path, req.Node, parentExec, dir)
}

// hostBuildDeployMembersBringUp wraps bringUpMembers (bundle_members.go) — the final step of a
// non-dry-run `charly bundle add`, unchanged.
func hostBuildDeployMembersBringUp(_ context.Context, req spec.DeployMembersRequest, _ buildEngineContext) (spec.DeployMembersReply, error) {
	return spec.DeployMembersReply{}, bringUpMembers(req.Node, "")
}

// hostBuildDeployDelResolve wraps resolveDelNode (bundle_add_cmd.go, UNCHANGED) — a pure read
// (charly.yml tree lookup + on-disk artifact probe), so it needs no ledger lock.
func hostBuildDeployDelResolve(_ context.Context, req spec.DeployDelResolveRequest, _ buildEngineContext) (spec.DeployDelResolveReply, error) {
	c := &deployDelCmd{Name: req.Name}
	node, kind, err := c.resolveDelNode()
	if err != nil {
		return spec.DeployDelResolveReply{}, err
	}
	return spec.DeployDelResolveReply{Node: node, Kind: kind}, nil
}

// hostBuildDeployNodeDelDispatch is the `charly bundle del` terminal step — the ledger lock +
// loadDeployPlugins + ResolveTarget + the teardown-gate flags + tearDownMembers + target.Del (the
// OLD deployDelCmd.Run's tail, unchanged in substance; the live ReverseRunner is never carried on
// the wire — a programmatic teardown needing a specific runner is resolved host-side, matching
// the OLD host_build_deploy_del.go's contract).
func hostBuildDeployNodeDelDispatch(ctx context.Context, req spec.DeployNodeDelDispatchRequest, _ buildEngineContext) (spec.DeployNodeDelDispatchReply, error) {
	paths, err := kit.DefaultLedgerPaths()
	if err != nil {
		return spec.DeployNodeDelDispatchReply{}, err
	}
	lock, err := kit.AcquireLedgerLock(paths)
	if err != nil {
		return spec.DeployNodeDelDispatchReply{}, err
	}
	defer lock.Release() //nolint:errcheck

	dir, err := getwdOrErr("deploy-node-del-dispatch")
	if err != nil {
		return spec.DeployNodeDelDispatchReply{}, err
	}
	if err := loadDeployPlugins(dir, req.Name, nil); err != nil {
		return spec.DeployNodeDelDispatchReply{}, err
	}

	utgt, err := ResolveTarget(req.Node, req.Name)
	if err != nil {
		return spec.DeployNodeDelDispatchReply{}, err
	}
	if tt, ok := utgt.(*externalDeployTarget); ok {
		tt.KeepRepoChanges = req.KeepRepoChanges
		tt.KeepServices = req.KeepServices
		tt.KeepImage = req.KeepImage
	}

	var memberErr error
	if !req.DryRun {
		memberErr = tearDownMembers(req.Node)
	}
	targetErr := utgt.Del(ctx, DelOpts{
		DryRun:    req.DryRun,
		AssumeYes: req.AssumeYes,
	})
	return spec.DeployNodeDelDispatchReply{}, errors.Join(memberErr, targetErr)
}

// getwdOrErr is the one-line os.Getwd + labeled-error wrap every K4 lane A seam needs.
func getwdOrErr(label string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("%s: getwd: %w", label, err)
	}
	return dir, nil
}

var _ = func() bool {
	registerHostBuilder(deployTreeResolveBuilderKind, typedHostBuilder(deployTreeResolveBuilderKind, hostBuildDeployTreeResolve))
	registerHostBuilder(deployNodeDispatchBuilderKind, typedHostBuilder(deployNodeDispatchBuilderKind, hostBuildDeployNodeDispatch))
	registerHostBuilder(deployMembersBringUpBuilderKind, typedHostBuilder(deployMembersBringUpBuilderKind, hostBuildDeployMembersBringUp))
	registerHostBuilder(deployDelResolveBuilderKind, typedHostBuilder(deployDelResolveBuilderKind, hostBuildDeployDelResolve))
	registerHostBuilder(deployNodeDelDispatchBuilderKind, typedHostBuilder(deployNodeDelDispatchBuilderKind, hostBuildDeployNodeDelDispatch))
	return true
}()
