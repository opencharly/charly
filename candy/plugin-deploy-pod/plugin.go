// Package deploypod is the charly DEPLOY plugin serving the `pod`
// deploy SUBSTRATE — `target: pod` (the DEFAULT substrate: a deployment run as a
// container image via quadlet/podman). It is the pod-substrate sibling of
// candy/plugin-deploy-vm: charly host-builds it and serves it OUT-OF-PROCESS over
// go-plugin gRPC (LocalTransport), then externalDeployTarget Invokes it (OpExecute)
// with the deployment's InstallPlan VIEWS + a venue descriptor, and the host's executor
// served on the broker.
//
// Unlike deploy:vm (whose plugin WALKS the plan inside the guest), pod bakes its install
// steps INTO the image at BUILD time. The host's pod lifecycle hook
// (pod_deploy_lifecycle.go) builds the overlay container image HOST-SIDE in PrepareVenue
// (the SAME core OCITarget/Generator build engine, in-process — nothing crosses gRPC, just
// like vm builds its disk host-side) and the bed runner / `charly start` then configs +
// starts the container. So there is NO per-step venue walk for pod: this plugin's Invoke
// does NOT call kit.WalkPlans — walking the add_candy steps on the host venue would be
// WRONG (they are already baked into the overlay image host-side). It returns an EMPTY
// DeployReply (no teardown reverse ops — pod teardown is `charly remove` + drop overlay
// images, owned by the host lifecycle hook's PostTeardown), keyed by the deploy name.
//
// The plugin exists to serve deploy:pod out-of-process (the uniform substrate model: pod
// is external like local/vm/android/k8s) and to acknowledge the Invoke; the real work
// (overlay build + container lifecycle) is the host's, exactly as the vm lifecycle hook
// owns the VM build+boot while plugin-deploy-vm owns only the plan walk.
//
// Dual-placement by construction: the SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS over
// go-plugin gRPC when they are not — placement is invisible above the registry.
package deploypod

import (
	"context"
	"embed"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.180.0001"

// NewProvider returns the deploypod provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the plugin's capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke acknowledges the deploy:pod Apply. The overlay container image was already built
// HOST-SIDE by the pod lifecycle hook's PrepareVenue (the core OCITarget build engine runs
// in-process on the host, nothing crosses the process boundary), so there is nothing to
// walk on a venue here — the plugin returns an EMPTY DeployReply (no reverse ops; pod
// teardown is `charly remove` + drop overlay, owned by the host hook's PostTeardown).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	venue, err := sdk.DecodeDeployVenue(req.GetEnvJson())
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod: decode venue: %w", err)
	}
	// Validate the plan views round-tripped (provenance), but do NOT walk them — pod bakes
	// its steps into the image host-side; walking them on the venue would be wrong.
	if _, err := sdk.DecodeInstallPlans(req.GetParamsJson()); err != nil {
		return nil, fmt.Errorf("plugin-deploy-pod: decode plans: %w", err)
	}

	candy := venue.DeployName
	if candy == "" {
		candy = "deploy-pod"
	}
	// No reverse ops: the overlay build is host-side and teardown is `charly remove` + drop
	// overlay images (the host lifecycle hook's PostTeardown), not a replayed step walk.
	return sdk.BuildDeployReply(nil, candy, calver)
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the deploy:pod capability (empty InputDef — the substrate carries no
// authored plugin_input) + its self-contained, load-gate-only CUE schema.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{Class: "deploy", Word: "pod", InputDef: ""}},
		schemaFS, "schema")
}
