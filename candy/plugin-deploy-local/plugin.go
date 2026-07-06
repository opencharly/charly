// Package deploylocal is the charly DEPLOY plugin serving the
// `local` deploy SUBSTRATE — `target: local` (and `host: user@machine` SSH local) deploys.
// It is the production consumer of the host-engine reverse channel: charly host-builds it
// and serves it OUT-OF-PROCESS over go-plugin gRPC (LocalTransport), then
// externalDeployTarget.Add Invokes it (OpExecute) with the deployment's InstallPlan VIEWS
// (the serializable per-step IR, with secrets injected + {{.Home}} resolved + each step's
// teardown ops captured host-side) + a venue descriptor, and the host's executor served on
// the broker (ShellExecutor for host:local, SSHExecutor for host:user@machine).
//
// Invoke dials BACK through the SDK Executor and hands the plans to kit.WalkPlans — the ONE
// shared deploy walk:
//
//   - plugin-renderable steps (Op write/cmd/download, File, ShellHook + the env.d
//     managed-block finalizer, ShellSnippet, ServicePackaged, ServiceCustom, RepoChange)
//     it EXECUTES itself via the F2 reverse legs (RunSystem/RunUser/PutFile/GetFile),
//     ECHOING the host-computed view.ReverseOps;
//   - host-engine steps (Builder/LocalPkgInstall/SystemPackages/act-Op/ExternalPlugin) it
//     drives over RunHostStep, folding in the host-returned reverse ops.
//
// It returns a DeployReply carrying the combined teardown ops the host records in the
// install ledger and replays at `charly bundle del` (record-and-replay). The deploy-class
// production sibling of candy/plugin-example-deploy (the F2/F3 witness).
//
// Dual-placement by construction: the SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS over
// go-plugin gRPC when they are not — placement is invisible above the registry.
package deploylocal

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
)

const calver = "2026.181.0001"

// NewProvider returns the deploylocal provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the deploy:local capability (empty InputDef — the substrate carries
// no authored plugin_input) + its self-contained, load-gate-only CUE schema, via
// sdk.NewMeta → BuildCapabilities.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "deploy", Word: "local", InputDef: ""}},
		nil)
}

type provider struct{ pb.UnimplementedProviderServer }

// Compile-time proof the SDK's reverse-channel Executor satisfies kit's deploy-walk
// surface — so the plugin hands its sdk.Executor straight to kit.WalkPlans (no adapter).
var _ kit.DeployExecutor = (*sdk.Executor)(nil)

// Invoke applies the deployment on the venue via the reverse channel + kit.WalkPlans, then
// returns the combined teardown ops + ledger record.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-local: %w", err)
	}
	plans, err := sdk.DecodeInstallPlans(req.GetParamsJson())
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-local: decode plans: %w", err)
	}
	venue, err := sdk.DecodeDeployVenue(req.GetEnvJson())
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-local: decode venue: %w", err)
	}

	reverseOps, err := kit.WalkPlans(ctx, exec, plans, kit.WalkOpts{})
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-local: %w", err)
	}

	// The ledger record is keyed by the deploy name (the host's externalDeployTarget keys
	// the DeployRecord on computeDeployID(name)); the candy field names the logical record
	// whose aggregated ReverseOps drive teardown.
	candy := venue.DeployName
	if candy == "" {
		candy = "deploy-local"
	}
	return sdk.BuildDeployReply(reverseOps, candy, calver)
}
