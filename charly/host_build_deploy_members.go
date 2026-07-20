package main

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

// host_build_deploy_members.go — the "deploy-members-up"/"deploy-members-down" F10 host-builders
// (K4-C walk port). bringUpMembers/tearDownMembers (bundle_members.go) are providerRegistry +
// ledger + subprocess-dependent (runCharlySubcommand re-entry over os.Args[0]) and STAY host-side
// unchanged; the plugin's walk reaches them once at the end of `charly bundle add` (bring-up) and
// once at the start of `charly bundle del` (tear-down) through these two thin seams — sharing ONE
// request/reply shape (R3), discriminated by the registered kind name.
const (
	deployMembersUpBuilderKind   = "deploy-members-up"
	deployMembersDownBuilderKind = "deploy-members-down"
)

func hostBuildDeployMembersUp(_ context.Context, req spec.DeployMembersRequest, _ buildEngineContext) (spec.DeployMembersReply, error) {
	return spec.DeployMembersReply{}, bringUpMembers(req.Node, "")
}

func hostBuildDeployMembersDown(_ context.Context, req spec.DeployMembersRequest, _ buildEngineContext) (spec.DeployMembersReply, error) {
	return spec.DeployMembersReply{}, tearDownMembers(req.Node)
}

var _ = func() bool {
	registerHostBuilder(deployMembersUpBuilderKind, typedHostBuilder(deployMembersUpBuilderKind, hostBuildDeployMembersUp))
	registerHostBuilder(deployMembersDownBuilderKind, typedHostBuilder(deployMembersDownBuilderKind, hostBuildDeployMembersDown))
	return true
}()
