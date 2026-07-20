package main

import (
	"context"
	"os"

	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_del_resolve.go — the "deploy-del-resolve" F10 host-builder (K4-C walk port).
// resolveDelNode (literal "host" / "vm:"-prefix legacy forms / a charly.yml tree entry / a
// ref-based pod-artifact probe) needs LoadUnified + the on-disk artifact probe, so it stays
// host-side; the plugin's `charly bundle del` calls this FIRST. Also connects the deployment's
// out-of-tree plugin candies (loadDeployPlugins), the SAME preamble deploy-tree-resolve runs for
// Add.
const deployDelResolveBuilderKind = "deploy-del-resolve"

func hostBuildDeployDelResolve(_ context.Context, req spec.DeployDelResolveRequest, _ buildEngineContext) (spec.DeployDelResolveReply, error) {
	if dir, err := os.Getwd(); err == nil && dir != "" {
		if err := loadDeployPlugins(dir, req.Name, nil); err != nil {
			return spec.DeployDelResolveReply{}, err
		}
	}
	c := &deployDelCmd{Name: req.Name}
	node, kind, err := c.resolveDelNode()
	if err != nil {
		return spec.DeployDelResolveReply{}, err
	}
	return spec.DeployDelResolveReply{Node: node, Kind: kind}, nil
}

var _ = func() bool {
	registerHostBuilder(deployDelResolveBuilderKind, typedHostBuilder(deployDelResolveBuilderKind, hostBuildDeployDelResolve))
	return true
}()
