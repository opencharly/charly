package main

import (
	"context"
	"os"

	"github.com/opencharly/spec/spec"
)

// host_build_deploy_plugins_connect.go — the "deploy-plugins-connect" F10 host-builder, the ONE
// host-only PREAMBLE the K1-LOADER RELOCATION witness (Unit D) leaves in place. candy/plugin-bundle
// now DRIVES loaderkit.LoadUnified ITSELF (plugin-side, over the reverse-channel LoaderExecutor —
// execLoaderExecutor + the "loader-*" host legs) to resolve the `charly bundle add` deploy tree, so
// the host no longer runs resolveTreeRoot for the walk. This seam only (a) connects the
// deployment's out-of-tree plugin candies (loadDeployPlugins — registry-coupled, a core Mechanism)
// BEFORE ResolveTarget can route to an external substrate, and (b) returns the project dir
// (os.Getwd — the SAME dir resolveTreeRoot uses everywhere else) the plugin passes to
// loaderkit.LoadUnified. root-venue-ssh is now read plugin-side from the loaded tree's stamped
// node.Descent (loaderkit.LoadUnified stamps it), needing no registry-backed nodeTraits host call.
const deployPluginsConnectBuilderKind = "deploy-plugins-connect"

func hostBuildDeployPluginsConnect(_ context.Context, req spec.DeployPluginsConnectRequest, _ buildEngineContext) (spec.DeployPluginsConnectReply, error) {
	dir, err := os.Getwd()
	if err != nil {
		return spec.DeployPluginsConnectReply{}, err
	}
	if err := loadDeployPlugins(dir, req.Path, req.AddCandy); err != nil {
		return spec.DeployPluginsConnectReply{}, err
	}
	return spec.DeployPluginsConnectReply{Dir: dir}, nil
}

var _ = func() bool {
	registerHostBuilder(deployPluginsConnectBuilderKind, typedHostBuilder(deployPluginsConnectBuilderKind, hostBuildDeployPluginsConnect))
	return true
}()
