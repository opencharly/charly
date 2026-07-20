package main

import (
	"context"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_tree_resolve.go — the "deploy-tree-resolve" F10 host-builder (K4-C walk
// port). candy/plugin-bundle now OWNS the `charly bundle add` dispatch CONTROL FLOW (the
// pre-order tree walk); resolveTreeRoot (reads LoadUnified, a core Mechanism a plugin cannot
// import) stays host-side, returning the WHOLE merged project+operator deploy tree so the plugin
// walks it itself via the already-pure sdk/deploykit ResolveNodePath/SplitDottedPath. Also
// connects the deployment's out-of-tree plugin candies (loadDeployPlugins) — the ONE
// per-invocation preamble every dispatch needs before ResolveTarget can route to an external
// substrate.
const deployTreeResolveBuilderKind = "deploy-tree-resolve"

func hostBuildDeployTreeResolve(_ context.Context, req spec.DeployTreeResolveRequest, _ buildEngineContext) (spec.DeployTreeResolveReply, error) {
	dir, err := os.Getwd()
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

	reply := spec.DeployTreeResolveReply{}
	if len(tree) > 0 {
		reply.Tree = make(map[string]*spec.Deploy, len(tree))
		for k, v := range tree {
			n := v
			reply.Tree[k] = &n
		}
	}
	// root_venue_ssh reports whether the resolved root's stamped descent traits are the "ssh"
	// venue (a vm root) — nodeTraits is registry-backed (a host-only mechanism), so the plugin
	// dispatches node-only on this signal instead of resolving traits itself.
	if node, _, nodeErr := deploykit.ResolveNodePath(tree, req.Path); nodeErr == nil && node != nil {
		reply.RootVenueSSH = nodeTraits(node).Venue == "ssh"
	}
	return reply, nil
}

var _ = func() bool {
	registerHostBuilder(deployTreeResolveBuilderKind, typedHostBuilder(deployTreeResolveBuilderKind, hostBuildDeployTreeResolve))
	return true
}()
