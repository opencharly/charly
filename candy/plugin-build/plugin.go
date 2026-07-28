// Package build is the importable form of charly's BUILD-DRIVE plugin: it OWNS the podman
// build drive (the build-order loop, the per-image build lock, the push, and the merge gate)
// for the two build words `build:box` (the `charly box build` engine) and `build:generate`
// (the `charly box generate` engine).
//
// OWNS THE DRIVE, RUNS THE RESOLVE PLUGIN-SIDE (K3 U6). resolveBuildEngine (resolve.go) runs the
// whole build-engine RESOLVE ITSELF — loaderkit.LoadUnified over the K1 loader legs, the vocab
// projection, the candy scan, buildkit.ResolveAllBox + deploykit.ComputeIntermediates/GlobalCandyOrder,
// render-prep, the envelope, and the drive-model (engine/platform/order/levels/descriptors/tunables) —
// reaching the host over a small `buildengine-*` HostBuild leg family ONLY for what a sdk-only candy
// cannot do (the bootstrap-delicate local scan, the git fetch, the build-time plugin connect, the
// host-fs prep). The layer MERGE crosses to verb:oci via InvokeProvider. The candy then runs podman
// directly — building each image (Containerfile piped over stdin), gating the inline merge on the
// box's MergeAuto, and pushing (podman) after merge. The podman exec happens IN the candy.
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
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.182.1600"

// NewProvider returns the build-drive provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the three build-drive capabilities (Class "build", words "box" +
// "generate" + "ensure", Phase "build") + the plugin's self-contained CUE schema (via
// sdk.NewMeta → BuildCapabilities). InputDef is "" for all three: the BuildRequest /
// BuildEnsureRequest is HOST-constructed (by BuildCmd / dispatchBuildEnsure / the box command
// plugin candy/plugin-box's generate handler), never user-authored in charly.yml, so there is
// no plugin_input to validate against a served schema. The self-contained #BuildDispatch def
// exists only to satisfy the non-empty-schema load gate + document the seam.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{
			{Class: "build", Word: "box", Phase: sdk.PhaseBuild},
			{Class: "build", Word: "generate", Phase: sdk.PhaseBuild},
			{Class: "build", Word: "ensure", Phase: sdk.PhaseBuild},
		},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke handles OpBuild for the build words. It resolves the host reverse channel
// (sdk.ExecutorForInvoke — the in-proc context executor when compiled-in, the go-plugin broker
// when served out-of-process) and drives the requested word: build:box runs the full
// build/merge/push drive (runBoxBuild), build:generate renders the Containerfile tree host-side
// and returns the written paths (runBoxGenerate) — both decode a host-constructed
// spec.BuildRequest and resolve+merge over HostBuild but run podman IN the candy. build:ensure
// (core-min wave 3, build-engine cluster relocation) decodes a spec.BuildEnsureRequest instead
// and drives the ensure-image orchestration (runBoxEnsure): ensure an image is present in local
// podman storage, falling back to a local (or remote-cached) build reached via the SAME
// in-process runBoxBuild this Invoke already owns for the box word — no new build seam. A build
// FAILURE rides spec.BuildReply.Error / spec.BuildEnsureReply.Error inside the returned JSON (the
// RPC succeeds); an infrastructure failure (no executor, unknown op/word) is returned as a Go
// error.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpBuild {
		return nil, fmt.Errorf("build: unsupported op %q (only %q)", req.GetOp(), sdk.OpBuild)
	}
	word := req.GetReserved()
	if word != "box" && word != "generate" && word != "ensure" {
		return nil, fmt.Errorf("build: unknown build word %q (want box|generate|ensure)", word)
	}
	ex, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("build %q: reach host reverse channel: %w", word, err)
	}

	if word == "ensure" {
		var ereq spec.BuildEnsureRequest
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &ereq); err != nil {
				return nil, fmt.Errorf("build ensure: decode BuildEnsureRequest: %w", err)
			}
		}
		ensureErr := runBoxEnsure(ctx, ex, ereq)
		out, err := json.Marshal(spec.BuildEnsureReply{Error: errString(ensureErr)})
		if err != nil {
			return nil, fmt.Errorf("build ensure: encode reply: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	}

	var breq spec.BuildRequest
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &breq); err != nil {
			return nil, fmt.Errorf("build %q: decode BuildRequest: %w", word, err)
		}
	}

	var written []string
	var buildErr error
	switch word {
	case "box":
		written, buildErr = runBoxBuild(ctx, ex, breq)
	case "generate":
		written, buildErr = runBoxGenerate(ctx, ex, breq)
	}

	out, err := json.Marshal(spec.BuildReply{Written: written, Error: errString(buildErr)})
	if err != nil {
		return nil, fmt.Errorf("build %q: encode reply: %w", word, err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}
