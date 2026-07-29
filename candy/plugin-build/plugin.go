// Package build is the importable form of charly's BUILD-DRIVE plugin: it OWNS the podman
// build drive (the build-order loop, the per-image build lock, the push, and the merge gate)
// for the two build words `build:box` (the `charly box build` engine) and `build:generate`
// (the `charly box generate` engine), plus `build:ensure` (ensure-image) and `build:pkg`
// (`charly box pkg` — native package-artifact builds for a candy's localpkg sources, K3
// build-tail move: the former hidden core `__box-pkg` reentry is DELETED).
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
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.182.1600"

// NewProvider returns the build-drive provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the four build-drive capabilities (Class "build", words "box" +
// "generate" + "ensure" + "pkg", Phase "build") + the plugin's self-contained CUE schema (via
// sdk.NewMeta → BuildCapabilities). InputDef is "" for all four: the BuildRequest /
// BuildEnsureRequest / BuildPkgRequest is HOST-constructed (by candy/plugin-box's dispatchBuild /
// dispatchBuildEnsure / dispatchPkg / the box command plugin's generate handler), never
// user-authored in charly.yml, so there is no plugin_input to validate against a served schema.
// The self-contained #BuildDispatch def exists only to satisfy the non-empty-schema load gate +
// document the seam.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{
			{Class: "build", Word: "box", Phase: sdk.PhaseBuild},
			{Class: "build", Word: "generate", Phase: sdk.PhaseBuild},
			{Class: "build", Word: "ensure", Phase: sdk.PhaseBuild},
			{Class: "build", Word: "pkg", Phase: sdk.PhaseBuild},
			{Class: "build", Word: "project", Phase: sdk.PhaseBuild},
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
	word := req.GetReserved()

	// build:project rides OpResolve (#55 step3 unit 3b) — the plugin-side replacement for the
	// deleted "resolved-project" HostBuild seam, reached via InvokeProvider peer-dispatch. Named
	// "project" (not "resolve") because the WORD may never collide with an sdk.Op* selector VALUE
	// (the F11 uniform-API gate, TestNoSinglePluginAPISurface): sdk.OpResolve == "resolve" already,
	// so a provider word of literally "resolve" would violate it — caught live by that gate.
	// Every other build word rides OpBuild, checked below.
	if word == "project" {
		switch req.GetOp() {
		case sdk.OpResolve:
			ex, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
			if err != nil {
				return nil, fmt.Errorf("build project: reach host reverse channel: %w", err)
			}
			var rreq spec.ResolvedProjectRequest
			if len(req.GetParamsJson()) > 0 {
				if err := json.Unmarshal(req.GetParamsJson(), &rreq); err != nil {
					return nil, fmt.Errorf("build project: decode ResolvedProjectRequest: %w", err)
				}
			}
			rp, err := resolveProjectEnvelope(ctx, ex, rreq)
			if err != nil {
				return nil, fmt.Errorf("build project: %w", err)
			}
			out, err := json.Marshal(rp)
			if err != nil {
				return nil, fmt.Errorf("build project: encode reply: %w", err)
			}
			return &pb.InvokeReply{ResultJson: out}, nil

		case sdk.OpValidate:
			// #55 step3 unit 3-I: the error-TOLERANT resolve half of the former validate-project
			// HostBuild seam, relocated onto the SAME "project" word via a second Op (resolve_project_tolerant.go).
			ex, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
			if err != nil {
				return nil, fmt.Errorf("build project: reach host reverse channel: %w", err)
			}
			var vreq spec.ValidateProjectRequest
			if len(req.GetParamsJson()) > 0 {
				if err := json.Unmarshal(req.GetParamsJson(), &vreq); err != nil {
					return nil, fmt.Errorf("build project: decode ValidateProjectRequest: %w", err)
				}
			}
			rp, diags := resolveProjectEnvelopeTolerant(ctx, ex, vreq)
			out, err := json.Marshal(spec.ValidateProjectReply{Project: &rp, Diagnostics: diags})
			if err != nil {
				return nil, fmt.Errorf("build project: encode reply: %w", err)
			}
			return &pb.InvokeReply{ResultJson: out}, nil

		default:
			return nil, fmt.Errorf("build project: unsupported op %q (want %q or %q)", req.GetOp(), sdk.OpResolve, sdk.OpValidate)
		}
	}

	if req.GetOp() != sdk.OpBuild {
		return nil, fmt.Errorf("build: unsupported op %q (only %q)", req.GetOp(), sdk.OpBuild)
	}
	if word != "box" && word != "generate" && word != "ensure" && word != "pkg" {
		return nil, fmt.Errorf("build: unknown build word %q (want box|generate|ensure|pkg|project)", word)
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

	if word == "pkg" {
		var preq spec.BuildPkgRequest
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &preq); err != nil {
				return nil, fmt.Errorf("build pkg: decode BuildPkgRequest: %w", err)
			}
		}
		written, pkgErr := runBoxPkg(ctx, ex, preq)
		out, err := json.Marshal(spec.BuildPkgReply{Written: written, Error: errString(pkgErr)})
		if err != nil {
			return nil, fmt.Errorf("build pkg: encode reply: %w", err)
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
