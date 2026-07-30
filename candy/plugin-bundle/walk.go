package bundle

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// walk.go — the K4-C WALK PORT: `charly bundle add`/`del`'s tree-walk CONTROL FLOW now runs
// plugin-side (P13-KERNEL walk port). `add`'s tree resolution now runs loaderkit.LoadUnified
// PLUGIN-SIDE (K1-LOADER RELOCATION witness, load_executor.go: resolveTreeViaLoader over the
// reverse-channel LoaderExecutor) — the host keeps only the deploy-plugins-connect preamble
// (loadDeployPlugins + project dir). resolveDelNode (LoadUnified-coupled) and
// deriveChildExecutorForPath (registry-coupled — deployTraitDescent needs the providerRegistry)
// stay host-side behind the deploy-del-resolve / resolve-target-add /
// deploy-node-del-dispatch / deploy-members-up / deploy-members-down seams. The plugin COMPILES the
// InstallPlans IN-PROC (K4-C shape-2, dispatch.go) and the per-node terminal ResolveTarget+Add
// reconstructs its OWN parentExec executor chain HOST-side (resolve-target-add) from the ancestor
// path/node lists this walk sends — a live DeployExecutor never
// crosses the wire, so this file holds NO executor plumbing at all: it walks spec.BundleNode's
// Children map directly (the SAME pre-order deploykit.WalkDeploymentTree drives host-side, with
// deterministic SortedNestedKeys ordering), threading ancestor context instead of an executor.

// Run executes `charly bundle add` (plugin-side walk; the deploy-add host-build seam it used to
// forward the WHOLE Run() to is retired).
func (c *BundleAddCmd) Run() error {
	// Unit D WITNESS: drive loaderkit.LoadUnified PLUGIN-SIDE (over the reverse-channel
	// execLoaderExecutor) to resolve the deploy tree, instead of the former host merged-tree read
	// seam — proving command:bundle → loaderkit.LoadUnified end-to-end. The host preamble only
	// connects out-of-tree deploy plugins + hands back the project dir; rootVenueSSH is read from
	// the loaded tree's stamped node.Descent (load_executor.go).
	tree, rootVenueSSH, dir, err := resolveTreeViaLoader(c.Name, c.AddCandy)
	if err != nil {
		return err
	}
	// Thread the project dir (host os.Getwd) + the loader-threaded ExternalDeploySubstrates DATA
	// snapshot onto the command so the per-node compile (dispatchOne → compileNodePlans) runs
	// entirely plugin-side — the K4-C shape-2 in-proc compile, no OpCompile round-trip.
	c.dir = dir
	c.externalSubstrates = fetchExternalSubstrates()

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
	// — FALSE: deployName is derived from the path (path, or c.Name at the root when path is empty),
	// so an empty path meant BOTH the deploy name AND classifyNodeTarget's "host"/"local" literal
	// check were lost — EVERY ref-based `charly bundle add <name> <ref>` with no existing charly.yml
	// entry (INCLUDING the literal `host` form) resolved target "pod" (the unconditional fallback)
	// and deployName "", then failed ResolveTarget with `deployment "": target "pod" ... not
	// connected` — a total block for this whole call shape. Reproduced live on this branch,
	// BEFORE this fix, for both `bundle add host <candy>` and `bundle add <fresh-name> <ref>`
	// (see this repo's CHANGELOG for the pasted repro). Passing c.Name as path lets
	// classifyNodeTarget's pathLeaf(path) check work (path="host" → target "local") and
	// dispatchOne's `if path != "" { deployName = path }` carry the real name through — node stays
	// nil (no charly.yml entry to resolve).
	if rootNode == nil {
		return c.dispatchOne(c.Name, nil, nil, nil)
	}

	// --node-only dispatches just the resolved node, skipping the nested tree walk. A VM root
	// is ALSO dispatched node-only: its nested target:pod children deploy IN the guest (the
	// host can't tree-walk a pod-in-VM), so the VM target's Add deploys them itself after the
	// VM is up (plugin-deploy-vm's PostApply). A host tree walk would wrongly try to deploy
	// them locally / double-deploy. rootVenueSSH is read plugin-side from the loaded root's
	// stamped node.Descent.Venue=="ssh" (loaderkit.LoadUnified stamps it — byte-identical to the
	// former host registry-backed nodeTraits(rootNode).Venue check); the legacy "vm:"-prefixed name
	// form is checked here too (a pure string check, no registry needed).
	if c.NodeOnly || rootVenueSSH || strings.HasPrefix(resolvedPath, "vm:") {
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

// walk performs a pre-order traversal of node's Children, dispatching each position via dispatchOne
// (compile in-proc → resolve-target-add seam) and threading the growing ancestor path/node lists to
// its descendants — the plugin-side analogue of the OLD in-core WalkDeploymentTree callback, minus
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

// dispatchOne handles ONE tree position: it resolves the node's target/vmEntity/opts/ref/addCandies/
// tag PLUGIN-SIDE (classifyNodeTarget/resolveVmEntity/resolveNodeOverlays/resolveNodeTemplate —
// pure functions, node_resolve.go), COMPILES the InstallPlans IN-PROC (compileNodePlans →
// compilePlansForRequest, NO OpCompile round-trip), stamps the deployID + AddCandies plugin-side,
// and — unless --dry-run (which prints the plans plugin-side and returns) — ships the ALREADY-
// COMPILED plans to the host over the ONE thin HostBuild("resolve-target-add") seam. The host does
// only the floor-M residue a plugin cannot: reconstruct the ancestor executor chain,
// loadConfigForDeploy, ResolveTarget + Add. This is the K4-C shape-2 double-bounce elimination.
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

	// Compile the InstallPlans IN-PROC (the shared compilePlansForRequest — no OpCompile hop).
	plans, base, candySet, err := c.compileNodePlans(target, refStr, tag, path, addCandies, vmEntity, opts.BuilderImageOverride)
	if err != nil {
		return err
	}

	// Stamp the deployID + union AddCandies (the former host dispatchNode tail — deploykit.
	// ComputeDeployID is pure; both fields round-trip through InstallPlanView to the host). Union —
	// don't clobber: the per-plan propagation already populated p.AddCandies with the overlay-candy
	// names (explicit add_candy + transitive deps); a plain overwrite with the user-facing addCandies
	// list would drop the transitive entries (an overlay declaring add_candy:[k3s-server] would ship
	// k3s-server but not its k3s base — runtime failure).
	deployID := deploykit.ComputeDeployID(base, candySet, addCandies)
	for _, p := range plans {
		if p == nil {
			continue
		}
		p.DeployID = deployID
		seen := make(map[string]bool, len(p.AddCandies))
		for _, al := range p.AddCandies {
			seen[al] = true
		}
		for _, al := range addCandies {
			if !seen[al] {
				p.AddCandies = append(p.AddCandies, al)
				seen[al] = true
			}
		}
	}

	if c.DryRun {
		return printPlans(plans, c.Format == "json")
	}

	// Ship the compiled plans (deployID/AddCandies already stamped) to the host for
	// ResolveTarget + DeployContext + Add. deployName is the node's identity (path, or c.Name at the
	// root when path is empty) — matching the former host dispatchNode.
	views := make([]spec.InstallPlanView, 0, len(plans))
	for _, p := range plans {
		if p != nil {
			views = append(views, deploykit.WireView(p))
		}
	}
	plansJSON, err := json.Marshal(views)
	if err != nil {
		return fmt.Errorf("bundle add: marshal compiled plans: %w", err)
	}
	deployName := c.Name
	if path != "" {
		deployName = path
	}

	return hostDeploySeamJSON("resolve-target-add", spec.DeployResolveTargetAddRequest{
		Path:             path,
		DeployName:       deployName,
		Node:             node,
		Target:           target,
		Dir:              c.dir,
		PlansJSON:        plansJSON,
		AncestorPaths:    ancestorPaths,
		AncestorNodes:    ancestorNodes,
		NodeOnly:         c.NodeOnly,
		Pull:             opts.Pull,
		Verify:           opts.Verify,
		WithServices:     opts.WithServices,
		AllowRepoChanges: opts.AllowRepoChanges,
		AllowRootTasks:   opts.AllowRootTasks,
		SkipIncompatible: opts.SkipIncompatible,
		AssumeYes:        c.AssumeYes,
		BuilderImage:     opts.BuilderImageOverride,
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

	// Resolve the merged deploy tree PLUGIN-SIDE (also connects the deployment's plugins) and
	// thread it into the seam as DATA — the #55 Cone A Unit 3a tree-threading that replaced the
	// host resolveDelNode's former core merged-tree read. A tree-absent project yields a nil
	// tree, which resolveDelNode handles via its non-tree fallbacks (vm-prefix / pod-artifact).
	tree, _, _, err := resolveTreeViaLoader(c.Name, nil)
	if err != nil {
		return err
	}
	treeJSON, err := json.Marshal(tree)
	if err != nil {
		return err
	}
	var dr spec.DeployDelResolveReply
	if err := hostDeploySeamJSON("deploy-del-resolve", spec.DeployDelResolveRequest{Name: c.Name, TreeJSON: treeJSON}, &dr); err != nil {
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
