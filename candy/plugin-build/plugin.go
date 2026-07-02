// Package build is the importable form of charly's BUILD-ENGINE DISPATCH plugin: a thin
// dispatch/echo over the F10 HostBuild reverse-channel seam, serving the two build words
// `build:box` (the `charly box build` engine) and `build:generate` (the `charly box generate`
// engine).
//
// PURE DISPATCH/ECHO seam (the group/substrate/candy echo pattern applied to build). The image
// build engine — the Generator, the OCITarget, the runtime Candy graph — is I/O-bound with
// unexported state and a huge blast radius, so it STAYS host-side in-process, UNCHANGED. This
// plugin therefore does NOT run the engine: its Invoke forwards the host-constructed BuildRequest
// (op.Params, verbatim) BACK to the host via Executor.HostBuild(kind, …) over the reverse channel,
// and ECHOES the host-builder's opaque BuildReply as its result. build:box → HostBuild("image");
// build:generate → HostBuild("generate"). Only the wire envelope (BuildRequest in, BuildReply out)
// crosses the seam.
//
// PLACEMENT — COMPILED-IN (listed in the embedded charly/charly.yml compiled_plugins:). `charly
// box build` / `charly box generate` dispatch it IN-PROCESS: the host threads the reverse channel
// onto the Invoke context (dispatchBuild → sdk.ContextWithExecutor), so ExecutorForInvoke reaches
// HostBuild WITHOUT a go-plugin broker. cmd/serve serves it out-of-process too for module-shape
// parity (one provider, two placements) — though the build words are dispatched compiled-in.
package build

import (
	"context"
	"embed"
	"fmt"

	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.182.1600"

// NewProvider returns the build-dispatch provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpBuild for the build words. It resolves the host reverse channel
// (sdk.ExecutorForInvoke — the in-proc context executor when compiled-in, the go-plugin broker
// when served out-of-process), maps the served word to its host-builder kind, and forwards the
// host-constructed BuildRequest (req.ParamsJson, verbatim) to Executor.HostBuild. The host-builder
// runs the engine in-process and returns the opaque BuildReply, which this ECHOES as the result.
// A build FAILURE rides BuildReply.Error inside the echoed JSON (the RPC succeeds); an
// infrastructure failure (no executor, RPC error) is returned as a Go error.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpBuild {
		return nil, fmt.Errorf("build dispatch: unsupported op %q (only %q)", req.GetOp(), sdk.OpBuild)
	}
	// Map the served word to its class-generic host-builder KIND (an action noun, disjoint from
	// the provider words per the F11 uniform-API gate): build:box → "image" (build an image),
	// build:generate → "containerfiles" (generate the .build/ Containerfile tree).
	var kind string
	switch req.GetReserved() {
	case "box":
		kind = "image"
	case "generate":
		kind = "containerfiles"
	default:
		return nil, fmt.Errorf("build dispatch: unknown build word %q (want box|generate)", req.GetReserved())
	}
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("build dispatch %q: reach host reverse channel: %w", req.GetReserved(), err)
	}
	reply, err := exec.HostBuild(ctx, kind, req.GetParamsJson())
	if err != nil {
		return nil, fmt.Errorf("build dispatch %q: host build: %w", req.GetReserved(), err)
	}
	return &pb.InvokeReply{ResultJson: reply}, nil
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the two build-dispatch capabilities (Class "build", words "box" + "generate",
// Phase "build"). InputDef is "" for both: the BuildRequest is HOST-constructed (by BuildCmd /
// GenerateCmd), never user-authored in charly.yml, so there is no plugin_input to validate against
// a served schema. The self-contained #BuildDispatch def exists only to satisfy the
// non-empty-schema load gate + document the seam.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{
			{Class: "build", Word: "box", Phase: sdk.PhaseBuild},
			{Class: "build", Word: "generate", Phase: sdk.PhaseBuild},
		},
		schemaFS, "schema")
}
