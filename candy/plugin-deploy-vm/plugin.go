// Package deployvm is the charly DEPLOY plugin serving the `vm`
// deploy SUBSTRATE — `target: vm` (a deployment applied INSIDE a running VM over SSH).
// It is the vm-substrate sibling of candy/plugin-deploy-local: charly host-builds it and
// serves it OUT-OF-PROCESS over go-plugin gRPC (LocalTransport), then externalDeployTarget
// Invokes it (OpExecute) with the deployment's InstallPlan VIEWS + a venue descriptor, and
// the host's executor served on the broker — for vm the GUEST SSHExecutor the host's vm
// lifecycle hook (vm_deploy_lifecycle.go) built after booting the domain, waiting for sshd /
// cloud-init, and ensuring charly is in the guest. The plugin dials BACK through the SDK
// Executor and hands the plans to kit.WalkPlans — the ONE shared deploy walk:
//
//   - plugin-renderable steps (Op write/cmd/download, File, ShellHook + the env.d
//     managed-block finalizer, ShellSnippet, ServicePackaged, ServiceCustom, RepoChange)
//     it EXECUTES itself via the F2 reverse legs (RunSystem/RunUser/PutFile/GetFile),
//     ECHOING the host-computed view.ReverseOps;
//   - host-engine steps (Builder/LocalPkgInstall/SystemPackages/act-Op/ExternalPlugin) it
//     drives over RunHostStep (host-side — builders run on the host's podman, artifacts
//     scp into the guest);
//   - a RebootStep (a `reboot: true` kernel-module layer) it also drives over RunHostStep,
//     where the host reboots the guest + waits for the deterministic boot_id change.
//
// Because the served executor IS the guest SSHExecutor, the SAME kit.WalkPlans that runs a
// local deploy on the host runs a vm deploy INSIDE THE GUEST — the difference is purely the
// executor's transport. {{.Home}} is resolved to the GUEST home host-side (the executor's
// ResolveHome targets the guest), so this plugin ships no substrate payload. It returns a
// DeployReply carrying the combined teardown ops the host records in the install ledger and
// replays at `charly bundle del` (record-and-replay). The host's vm lifecycle hook owns the
// VM lifecycle (boot/destroy/console/ssh + the nested pod-in-guest orchestration); this
// plugin owns ONLY the plan WALK.
//
// Dual-placement by construction: the SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS over
// go-plugin gRPC when they are not — placement is invisible above the registry.
package deployvm

import (
	"context"
	"embed"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.180.0001"

// NewProvider returns the deployvm provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the deploy:vm capability (empty InputDef — the substrate carries no
// authored plugin_input) + its self-contained, load-gate-only CUE schema, via
// sdk.NewMeta → BuildCapabilities.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{{Class: "deploy", Word: "vm", InputDef: "", Lifecycle: true}},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Compile-time proof the SDK's reverse-channel Executor satisfies kit's deploy-walk
// surface — so the plugin hands its sdk.Executor straight to kit.WalkPlans (no adapter).
var _ kit.DeployExecutor = (*sdk.Executor)(nil)

// Invoke applies the deployment INSIDE THE GUEST via the reverse channel + kit.WalkPlans,
// then returns the combined teardown ops + ledger record.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	// The vm substrate venue lifecycle Ops (prepare-venue/post-apply/start/stop/status/logs/shell/
	// rebuild/teardown) are IMPLEMENTED here (lifecycle.go) over GENERIC seams — sdk/kit for the
	// ssh-config stanza + guest waits + charly delivery, HostBuild("cli") for `charly vm …`, the
	// reverse channel for guest ops — consuming only the host-resolved spec.LifecyclePrepareInput.
	if isLifecycleOp(req.GetOp()) {
		return invokeLifecycle(ctx, req)
	}
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm: %w", err)
	}
	plans, err := sdk.DecodeInstallPlans(req.GetParamsJson())
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm: decode plans: %w", err)
	}
	venue, err := sdk.DecodeDeployVenue(req.GetEnvJson())
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm: decode venue: %w", err)
	}

	reverseOps, err := kit.WalkPlans(ctx, exec, plans, kit.WalkOpts{})
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm: %w", err)
	}

	// The ledger record is keyed by the deploy name (the host's externalDeployTarget keys
	// the DeployRecord on computeDeployID(name)); the candy field names the logical record
	// whose aggregated ReverseOps drive teardown.
	candy := venue.DeployName
	if candy == "" {
		candy = "deploy-vm"
	}
	return sdk.BuildDeployReply(reverseOps, candy, calver)
}
