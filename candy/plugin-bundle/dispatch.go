package bundle

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// dispatch.go — K4 lane A: the `charly bundle add` / `charly bundle del` dispatch CONTROL FLOW,
// moved out of charly core (the former deployAddCmd.Run / deployDelCmd.Run in
// charly/bundle_add_cmd.go). This file owns the target-path resolve, the pre-order tree walk, and
// the ancestor-chain bookkeeping — all pure Go over spec.BundleNode + the already-pure
// sdk/deploykit primitives (ResolveNodePath/SplitDottedPath, importable from either side of the
// plugin boundary). It reaches the host for exactly what a plugin cannot do itself: resolving the
// deploy tree (LoadUnified, a core Mechanism) via "deploy-tree-resolve"; the per-node
// resolve+compile+ResolveTarget+Add terminal step via "deploy-node-dispatch" (the RDD-spike-proven
// keystone seam — a real charly bundle __spike-add/del round-trip against check-preempt-local
// proved this seam sufficient, no new venue-scoped-executor protocol needed); bringing up sibling
// members via "deploy-members-bring-up"; resolving a del target via "deploy-del-resolve"; and the
// del terminal step via "deploy-node-del-dispatch". See charly/host_build_deploy_dispatch.go.

// runDeployAdd is `charly bundle add`'s new Run() body — mirrors the OLD deployAddCmd.Run
// EXACTLY (same branch structure, same resolvedPath/ancestor semantics), just reaching the host
// via seams instead of running in-process.
func runDeployAdd(c *BundleAddCmd) error {
	treeReqJSON, err := json.Marshal(spec.DeployTreeResolveRequest{Path: c.Name, AddCandy: c.AddCandy})
	if err != nil {
		return err
	}
	treeResJSON, err := cmdExec.HostBuild(cmdCtx, "deploy-tree-resolve", treeReqJSON)
	if err != nil {
		return fmt.Errorf("bundle add: deploy-tree-resolve: %w", err)
	}
	var treeReply spec.DeployTreeResolveReply
	if err := json.Unmarshal(treeResJSON, &treeReply); err != nil {
		return fmt.Errorf("bundle add: decode deploy-tree-resolve reply: %w", err)
	}

	targetPath := c.Name
	tree := valueTree(treeReply.Tree)
	var rootNode *spec.BundleNode
	var resolvedPath string
	var ancestorPaths []string
	var ancestorNodes []spec.BundleNode
	if tree != nil {
		if n, ancestors, nodeErr := deploykit.ResolveNodePath(tree, targetPath); nodeErr == nil {
			rootNode = n
			resolvedPath = targetPath
			segments := deploykit.SplitDottedPath(targetPath)
			for i, anc := range ancestors {
				ancestorPaths = append(ancestorPaths, strings.Join(segments[:i+1], "."))
				ancestorNodes = append(ancestorNodes, *anc)
			}
		}
	}

	// rootNode nil (ref-based deploy with no charly.yml entry) falls through to a single
	// dispatch at the EMPTY path — matches the old Run() exactly (resolvedPath is only ever set
	// inside the successful ResolveNodePath branch above).
	if rootNode == nil {
		return dispatchNodeSeam(c, resolvedPath, nil, nil, nil)
	}

	// --node-only, OR a vm root (nested pods deploy IN the guest — the host can't tree-walk a
	// pod-in-VM), OR the legacy "vm:"-prefixed name form: dispatch just the resolved node.
	if c.NodeOnly || treeReply.RootVenueSSH || strings.HasPrefix(resolvedPath, "vm:") {
		return dispatchNodeSeam(c, resolvedPath, rootNode, ancestorPaths, ancestorNodes)
	}

	if err := walkAndDispatch(c, resolvedPath, rootNode, ancestorPaths, ancestorNodes); err != nil {
		return err
	}

	if c.DryRun {
		return nil
	}
	return membersBringUpSeam(rootNode)
}

// walkAndDispatch is the plugin's OWN pre-order tree walk (parents before children, sorted-key
// deterministic — mirroring sdk/deploykit.WalkDeploymentTree's semantics exactly), threading
// ANCESTOR PATH+NODE DATA (never a live executor — that stays host-side, reconstructed per node
// via deriveChildExecutorForPath, which is NOT pure — it consults the provider registry) instead
// of a DeployExecutor.
func walkAndDispatch(c *BundleAddCmd, path string, node *spec.BundleNode, ancestorPaths []string, ancestorNodes []spec.BundleNode) error {
	if err := dispatchNodeSeam(c, path, node, ancestorPaths, ancestorNodes); err != nil {
		return err
	}
	if node == nil || len(node.Children) == 0 {
		return nil
	}
	childAncestorPaths := append(append([]string{}, ancestorPaths...), path)
	childAncestorNodes := append(append([]spec.BundleNode{}, ancestorNodes...), *node)
	keys := make([]string, 0, len(node.Children))
	for k := range node.Children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		childPath := path + "." + k
		if err := walkAndDispatch(c, childPath, node.Children[k], childAncestorPaths, childAncestorNodes); err != nil {
			return err
		}
	}
	return nil
}

// dispatchNodeSeam calls the per-node "deploy-node-dispatch" HostBuild seam — the keystone: the
// host reconstructs parentExec from ancestorPaths/ancestorNodes then runs dispatchNode
// (resolve+compile+ResolveTarget+Add) UNCHANGED.
func dispatchNodeSeam(c *BundleAddCmd, path string, node *spec.BundleNode, ancestorPaths []string, ancestorNodes []spec.BundleNode) error {
	req := spec.DeployNodeDispatchRequest{
		Path:             path,
		Node:             node,
		AncestorPaths:    ancestorPaths,
		AncestorNodes:    ancestorNodes,
		Ref:              c.Ref,
		AddCandy:         c.AddCandy,
		Tag:              c.Tag,
		DryRun:           c.DryRun,
		Format:           c.Format,
		Pull:             c.Pull,
		Verify:           c.Verify,
		WithServices:     c.WithServices,
		AllowRepoChanges: c.AllowRepoChanges,
		AllowRootTasks:   c.AllowRootTasks,
		SkipIncompatible: c.SkipIncompatible,
		BuilderImage:     c.BuilderImage,
		AssumeYes:        c.AssumeYes,
		Disposable:       c.Disposable,
		Lifecycle:        c.Lifecycle,
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = cmdExec.HostBuild(cmdCtx, "deploy-node-dispatch", reqJSON)
	return err
}

// membersBringUpSeam calls "deploy-members-bring-up" (bringUpMembers) once, at the end of a
// non-dry-run add — mirrors Run()'s final step exactly.
func membersBringUpSeam(node *spec.BundleNode) error {
	reqJSON, err := json.Marshal(spec.DeployMembersRequest{Node: node})
	if err != nil {
		return err
	}
	_, err = cmdExec.HostBuild(cmdCtx, "deploy-members-bring-up", reqJSON)
	return err
}

// valueTree converts the wire reply's pointer-valued map (spec.DeployTreeResolveReply.Tree is
// map[string]*Deploy, matching the deploy.cue nested:/peer: precedent) into the value-valued map
// deploykit.ResolveNodePath expects.
func valueTree(tree map[string]*spec.BundleNode) map[string]spec.BundleNode {
	if tree == nil {
		return nil
	}
	out := make(map[string]spec.BundleNode, len(tree))
	for k, v := range tree {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

// runDeployDel is `charly bundle del`'s new Run() body — mirrors the OLD deployDelCmd.Run's
// resolve-then-teardown sequence, split across two seams (safe to split: resolveDelNode is a pure
// read — a charly.yml tree lookup + an on-disk artifact probe, no ledger mutation — so it needs
// no ledger lock; the lock is acquired HOST-side inside deploy-node-del-dispatch, spanning exactly
// the ResolveTarget+Del sequence that touches the ledger, same as the ledger's OWN actual
// correctness requirement).
func runDeployDel(c *BundleDelCmd) error {
	resolveReqJSON, err := json.Marshal(spec.DeployDelResolveRequest{Name: c.Name})
	if err != nil {
		return err
	}
	resolveResJSON, err := cmdExec.HostBuild(cmdCtx, "deploy-del-resolve", resolveReqJSON)
	if err != nil {
		return fmt.Errorf("bundle del: deploy-del-resolve: %w", err)
	}
	var resolveReply spec.DeployDelResolveReply
	if err := json.Unmarshal(resolveResJSON, &resolveReply); err != nil {
		return fmt.Errorf("bundle del: decode deploy-del-resolve reply: %w", err)
	}

	dispatchReq := spec.DeployNodeDelDispatchRequest{
		Name:            c.Name,
		Node:            resolveReply.Node,
		AssumeYes:       c.AssumeYes,
		KeepRepoChanges: c.KeepRepoChanges,
		KeepServices:    c.KeepServices,
		KeepImage:       c.KeepImage,
		DryRun:          c.DryRun,
	}
	dispatchReqJSON, err := json.Marshal(dispatchReq)
	if err != nil {
		return err
	}
	_, err = cmdExec.HostBuild(cmdCtx, "deploy-node-del-dispatch", dispatchReqJSON)
	return err
}
