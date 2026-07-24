package bundle

import (
	"errors"
	"sort"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// walk.go — the K4-C WALK PORT: `charly bundle add`/`del`'s tree-walk CONTROL FLOW now runs
// plugin-side (P13-KERNEL walk port). resolveTreeRoot/resolveDelNode (LoadUnified-coupled) and
// deriveChildExecutorForPath (registry-coupled — deployTraitDescent needs the providerRegistry)
// stay host-side behind the deploy-tree-resolve / deploy-del-resolve / deploy-node-dispatch /
// deploy-node-del-dispatch / deploy-members-up / deploy-members-down seams. The per-node terminal
// step (resolve+compile+ResolveTarget+Add) reconstructs its OWN parentExec executor chain
// HOST-side from the ancestor path/node lists this walk sends — a live DeployExecutor never
// crosses the wire, so this file holds NO executor plumbing at all: it walks spec.BundleNode's
// Children map directly (the SAME pre-order deploykit.WalkDeploymentTree drives host-side, with
// deterministic SortedNestedKeys ordering), threading ancestor context instead of an executor.

// Run executes `charly bundle add` (plugin-side walk; the deploy-add host-build seam it used to
// forward the WHOLE Run() to is retired).
func (c *BundleAddCmd) Run() error {
	var tr spec.DeployTreeResolveReply
	if err := hostDeploySeamJSON("deploy-tree-resolve", spec.DeployTreeResolveRequest{
		Path:     c.Name,
		AddCandy: c.AddCandy,
	}, &tr); err != nil {
		return err
	}

	tree := make(map[string]spec.BundleNode, len(tr.Tree))
	for k, v := range tr.Tree {
		if v != nil {
			tree[k] = *v
		}
	}

	// Resolve the named root + any dotted-path subtree the user targeted. Supports three call
	// shapes: `charly bundle add host` (legacy; root = "host"), `charly bundle add
	// openclaw-stack` (v2 root with children), `charly bundle add openclaw-stack.web.db` (v2
	// subtree).
	rootNode, ancestors, resolveErr := deploykit.ResolveNodePath(tree, c.Name)
	var resolvedPath string
	var ancestorPaths []string
	var ancestorNodes []spec.BundleNode
	if resolveErr == nil {
		resolvedPath = c.Name
		segments := deploykit.SplitDottedPath(resolvedPath)
		for i, anc := range ancestors {
			ancestorPaths = append(ancestorPaths, strings.Join(segments[:i+1], "."))
			var n spec.BundleNode
			if anc != nil {
				n = *anc
			}
			ancestorNodes = append(ancestorNodes, n)
		}
	} else {
		rootNode = nil
	}

	// When rootNode is nil (ref-based deploy with no charly.yml entry, e.g. `charly bundle add
	// foo ./path/to/box.yml`, OR the literal "host" name, which never has a tree entry) fall
	// through to the single-dispatch path.
	//
	// R1 fix (found live while RDD-verifying an unrelated K5-A/W4 cutover): this used to pass
	// path="" unconditionally, on the claim that "deployName still resolves to c.Name host-side"
	// — FALSE in the current (post-P13-walk-port) architecture, where the host reconstructs a
	// FRESH deployAddCmd per dispatch from req.Path alone (runDeployNodeDispatch), so an empty
	// path meant BOTH the deploy name AND classifyNodeTarget's "host"/"local" literal check were
	// lost — EVERY ref-based `charly bundle add <name> <ref>` with no existing charly.yml entry
	// (INCLUDING the literal `host` form) resolved target "pod" (the unconditional fallback) and
	// deployName "", then failed ResolveTarget with `deployment "": target "pod" ... not
	// connected` — a total block for this whole call shape. Reproduced live on this branch,
	// BEFORE this fix, for both `bundle add host <candy>` and `bundle add <fresh-name> <ref>`
	// (see this repo's CHANGELOG for the pasted repro). Passing c.Name as path lets
	// classifyNodeTarget's pathLeaf(path) check work (path="host" → target "local") and
	// compileNode's/dispatchNode's `if path != "" { deployName = path }` carry the real name
	// through — node stays nil (no charly.yml entry to resolve).
	if rootNode == nil {
		return c.dispatchOne(c.Name, nil, nil, nil)
	}

	// --node-only dispatches just the resolved node, skipping the nested tree walk. A VM root
	// is ALSO dispatched node-only: its nested target:pod children deploy IN the guest (the
	// host can't tree-walk a pod-in-VM), so the VM target's Add deploys them itself after the
	// VM is up (plugin-deploy-vm's PostApply). A host tree walk would wrongly try to deploy
	// them locally / double-deploy. tr.RootVenueSSH is the host-resolved (registry-backed)
	// nodeTraits(rootNode).Venue=="ssh" check; the legacy "vm:"-prefixed name form is checked
	// here too (a pure string check, no registry needed).
	if c.NodeOnly || tr.RootVenueSSH || strings.HasPrefix(resolvedPath, "vm:") {
		return c.dispatchOne(resolvedPath, rootNode, ancestorPaths, ancestorNodes)
	}

	if err := c.walk(resolvedPath, rootNode, ancestorPaths, ancestorNodes); err != nil {
		return err
	}

	// Operator deploy path: bring up any sibling members (companion deployments) ALONGSIDE the
	// root on the shared `charly` network. A dry-run skips bring-up (nothing real was deployed).
	if c.DryRun {
		return nil
	}
	return hostDeploySeamJSON("deploy-members-up", spec.DeployMembersRequest{Node: rootNode}, nil)
}

// walk performs a pre-order traversal of node's Children, dispatching each position via the
// deploy-node-dispatch seam and threading the growing ancestor path/node lists to its
// descendants — the plugin-side analogue of the OLD in-core WalkDeploymentTree callback, minus
// any executor plumbing (see the file header).
func (c *BundleAddCmd) walk(path string, node *spec.BundleNode, ancestorPaths []string, ancestorNodes []spec.BundleNode) error {
	if err := c.dispatchOne(path, node, ancestorPaths, ancestorNodes); err != nil {
		return err
	}
	if node == nil || len(node.Children) == 0 {
		return nil
	}
	childAncestorPaths := append(append([]string(nil), ancestorPaths...), path)
	childAncestorNodes := append(append([]spec.BundleNode(nil), ancestorNodes...), *node)
	for _, k := range sortedChildKeys(node.Children) {
		child := node.Children[k]
		childPath := k
		if path != "" {
			childPath = path + "." + k
		}
		if err := c.walk(childPath, child, childAncestorPaths, childAncestorNodes); err != nil {
			return err
		}
	}
	return nil
}

func sortedChildKeys(children map[string]*spec.BundleNode) []string {
	out := make([]string, 0, len(children))
	for k := range children {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// dispatchOne invokes the deploy-node-dispatch seam for ONE tree position.
// target/vmEntity/ref/add_candy/tag/the resolved gate opts are ALL RESOLVED
// HERE (classifyNodeTarget/resolveVmEntity/resolveNodeOverlays/
// resolveNodeTemplate, W4 pure-helpers relocation, node_resolve.go) — pure
// functions of node+path+CLI-flags with no executor dependency (the ONE
// LoadUnified-coupled piece, the kind:local template lookup inside
// resolveNodeTemplate, reaches back to the host over the EXISTING
// "deploy-entity-resolve" seam) — so the host-side dispatch no longer
// recomputes any of them; it trusts every field as sent.
func (c *BundleAddCmd) dispatchOne(path string, node *spec.BundleNode, ancestorPaths []string, ancestorNodes []spec.BundleNode) error {
	target := deploykit.ClassifyNodeTarget(node, path)
	vmEntity := resolveVmEntity(path, node)

	opts, refStr, addCandies, tag, err := c.resolveNodeOverlays(path, node)
	if err != nil {
		return err
	}
	addCandies, opts, err = resolveNodeTemplate(target, path, node, addCandies, opts)
	if err != nil {
		return err
	}

	return hostDeploySeamJSON("deploy-node-dispatch", spec.DeployNodeDispatchRequest{
		Path:             path,
		Node:             node,
		AncestorPaths:    ancestorPaths,
		AncestorNodes:    ancestorNodes,
		Ref:              refStr,
		AddCandy:         addCandies,
		Tag:              tag,
		DryRun:           c.DryRun,
		NodeOnly:         c.NodeOnly,
		Format:           c.Format,
		Pull:             opts.Pull,
		Verify:           opts.Verify,
		WithServices:     opts.WithServices,
		AllowRepoChanges: opts.AllowRepoChanges,
		AllowRootTasks:   opts.AllowRootTasks,
		SkipIncompatible: opts.SkipIncompatible,
		BuilderImage:     opts.BuilderImageOverride,
		AssumeYes:        c.AssumeYes,
		Disposable:       c.Disposable,
		Lifecycle:        c.Lifecycle,
		Target:           target,
		VmEntity:         vmEntity,
	}, nil)
}

// Run executes `charly bundle del` (plugin-side; the deploy-del host-build seam it used to
// forward the WHOLE Run() to is retired). The ledger lock spans resolve → members-down →
// node-del-dispatch — kit.AcquireLedgerLock is a pure sdk/kit filesystem primitive, so acquiring
// it plugin-side (the compiled-in placement shares charly's own process/filesystem) reproduces
// the SAME lock scope the OLD in-core Run() held.
func (c *BundleDelCmd) Run() error {
	paths, err := kit.DefaultLedgerPaths()
	if err != nil {
		return err
	}
	lock, err := kit.AcquireLedgerLock(paths)
	if err != nil {
		return err
	}
	defer lock.Release() //nolint:errcheck

	var dr spec.DeployDelResolveReply
	if err := hostDeploySeamJSON("deploy-del-resolve", spec.DeployDelResolveRequest{Name: c.Name}, &dr); err != nil {
		return err
	}

	// Tear down any sibling members (companion deployments) FIRST — the reverse of
	// bringUpMembers (root up → members up; members down → root down). Best-effort. Skipped on
	// a dry-run.
	var memberErr error
	if !c.DryRun {
		memberErr = hostDeploySeamJSON("deploy-members-down", spec.DeployMembersRequest{Node: dr.Node}, nil)
	}

	targetErr := hostDeploySeamJSON("deploy-node-del-dispatch", spec.DeployNodeDelDispatchRequest{
		Name:            c.Name,
		Node:            dr.Node,
		AssumeYes:       c.AssumeYes,
		KeepRepoChanges: c.KeepRepoChanges,
		KeepServices:    c.KeepServices,
		KeepImage:       c.KeepImage,
		DryRun:          c.DryRun,
	}, nil)

	return errors.Join(memberErr, targetErr)
}
