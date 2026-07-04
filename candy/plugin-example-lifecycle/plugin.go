// Package examplelifecycle is the charly example deploy-substrate plugin (F6) — an importable, dual-placement root package —
// that brings its OWN host-side venue LIFECYCLE over the wire. Beyond the deploy walk (OpExecute),
// it serves the F6 substrate-lifecycle Ops: OpPrepareVenue returns a self-contained VenueDescriptor
// the HOST re-materializes into a real DeployExecutor (here a host-local ShellExecutor) — the live
// executor never crosses the wire — plus Start/Stop/Status/PostApply/PostTeardown/Rebuild/etc. and
// the generalized OpPreresolve. NOT in compiled_plugins (out-of-process only): the witness that a
// substrate plugin the host was not built with can drive a venue lifecycle host→plugin. The channel
// M4 reuses to externalize the pod/vm substrate lifecycles.
package examplelifecycle

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.181.0001"

// NewProvider returns the examplelifecycle provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the plugin's capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke dispatches the deploy walk (OpExecute), the generalized preresolver (OpPreresolve), and the
// F6 substrate-lifecycle Ops. The lifecycle methods carry name/node/opts in params_json; the venue
// methods return a VenueDescriptor the host re-materializes.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetOp() {
	case sdk.OpPrepareVenue:
		// Host-local venue: the host re-materializes a ShellExecutor from this descriptor.
		out, err := json.Marshal(spec.VenueDescriptor{Kind: "shell"})
		if err != nil {
			return nil, err
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	case sdk.OpTeardownExecutor:
		// Empty descriptor → the host keeps its ResolveTarget-selected executor.
		out, _ := json.Marshal(spec.VenueDescriptor{})
		return &pb.InvokeReply{ResultJson: out}, nil
	case sdk.OpArtifactKey:
		out, _ := json.Marshal(map[string]string{"key": ""})
		return &pb.InvokeReply{ResultJson: out}, nil
	case sdk.OpStatus:
		// A minimal healthy status (the host decodes StatusInfo).
		out, _ := json.Marshal(map[string]any{"state": "active (running)", "running": true})
		return &pb.InvokeReply{ResultJson: out}, nil
	case sdk.OpStart, sdk.OpStop, sdk.OpPostApply, sdk.OpPostTeardown, sdk.OpLogs, sdk.OpShell, sdk.OpRebuild:
		// Host-local no-op lifecycle legs (the example holds no real venue state).
		return &pb.InvokeReply{ResultJson: []byte("{}")}, nil
	case sdk.OpPreresolve:
		// The generalized host-side preresolver: ship an opaque marker the host stores in
		// DeployVenue.Substrate (proving the wire-backed preresolver path).
		out, _ := json.Marshal(map[string]string{"examplelifecycle_preresolved": "ok"})
		return &pb.InvokeReply{ResultJson: out}, nil
	case sdk.OpExecute:
		// The deploy walk: a host-local no-op ack (the example provisions nothing).
		return sdk.BuildDeployReply(nil, "plugin-example-lifecycle", calver)
	default:
		return nil, fmt.Errorf("examplelifecycle: unsupported op %q", req.GetOp())
	}
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the examplelifecycle deploy substrate. (The F6 lifecycle Ops are dispatched
// on the SAME Provider.Invoke — no separate capability surface; the host registers a wire-backed
// substrateLifecycle for this substrate at plugin-load.)
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{Class: "deploy", Word: "examplelifecycle", InputDef: "#ExamplelifecycleInput", Lifecycle: true, Preresolve: true}},
		schemaFS, "schema")
}
