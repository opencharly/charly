package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// host_build_render_seam.go — the "render-seam" host-builder (#67 render-DRIVE move).
// plugin-build's deploykit.Generator render calls back to the host for the REMAINING
// host-coupled seams (EmitPluginOp, inline-builder, ensure-builders) via
// HostBuild("render-seam", RenderSeamRequest{Method, Params}). This builder dispatches by
// Method to the corresponding CORE function — the EXACT funcs the core toDeploykit closures
// call — so the render is byte-identical to the pre-move core render (byte-parity by
// construction). The rich inputs (spec types — Builder, BuildStageContext, Op) ride the opaque
// Params bytes; the host unmarshals + calls. The live *Generator (gen.Boxes/gen.Candies/
// gen.Config/gen.Dir) comes from the per-dir renderGenCache populated by the buildengine-prep leg (one gen
// per dir per process) — no per-call reload.
//
// K3 render-seam production move: RenderService, the two detection/external builder resolves,
// ValidateEgress, and RewriteHeaderCopy were PURE providerRegistry.resolve+Invoke dispatch (or
// pure data + host-fs I/O over the CandyModel envelope) — proven to need no host callback at
// all (RDD-spiked live), so candy/plugin-build now calls them directly and their cases here are
// GONE (their host functions stay: RenderService/egressValidate/validateTextEgress still serve
// OTHER core callers — host_build_render_service.go, install_ledger.go, etc. —
// only the RENDER-SEAM's dispatch of them is dead). LocalPkg is ALSO GONE (W3): its render-seam
// claim of a genuine host dependency was STALE — CompileLocalPkgStep operates on CandyModel +
// ResolvedBox, both ALREADY present in the plugin's own dg.Candies/dg.Boxes (populated from the
// envelope), so RenderLocalPkgImageInstall now runs directly in candy/plugin-build (deploykit's
// NewRenderGeneratorFromProject wires it without a host round-trip). EmitPluginOp is GONE too
// (P8b): its "only charly core can type-assert a BUILTIN provider's ProvisionActor concrete type"
// claim FAILED the boundary law — a package-main prov.(ProvisionActor) branch is a concrete-type
// leak, not a permanent seam. A state-provision verb now serves OpEmit UNIFORMLY and self-declares
// its act shell via EmitReply.ActScript, so candy/plugin-build dispatches every verb through
// InvokeProvider(OpEmit) directly (render_generator_from_project.go), no host callback. The 2
// remaining cases have a genuine host-only dependency: EnsureBuilders/InlineBuilder need the live
// loader's scan+connect machinery (rides K1, #40) AND the provider registry (a permanent kernel
// M-mechanism — see CLAUDE.md "The kernel/plugin boundary law").
//
// #55 step3 3-II CONFIRMED (not moved): this seam's EnsureBuilders/InlineBuilder logic is
// UNRELATED to that cutover's scope (the pod-overlay envelope relocation) — reviewed and left in
// place verbatim; only loadRenderGen's cache-miss fallback below changed (the expensive
// NewGenerator it called is deleted — see that function's own comment).

// renderGenCache holds the live *Generator per project dir for the render-seam host-builder.
// Populated by the buildengine-prep host leg (hostBuildPrep, K3 U6 — via the cheap
// newCandyScanGenerator now; #55 step3 3-II deleted the expensive NewGenerator this floor used to
// run); read by hostBuildRenderSeam. One entry per dir per process — a single `charly box build`
// is one process, so the cache holds the one gen the prep leg loaded (render-prep already run).
var renderGenCache sync.Map

// loadRenderGen returns the cached *Generator for dir, falling back to the cheap
// newCandyScanGenerator (default opts) if the cache is empty (defensive — the buildengine-prep
// leg always populates it first). #55 step3 3-II: the expensive NewGenerator (full
// buildkit.ResolveAllBox + intermediates + render-prep) is DELETED — every normal build:box/
// build:generate call already populates renderGenCache unconditionally via hostBuildPrep, so this
// fallback is provably unreachable in production; kept as the cheap belt-and-suspenders default
// rather than the deleted expensive one.
func loadRenderGen(dir string) *Generator {
	if v, ok := renderGenCache.Load(dir); ok {
		return v.(*Generator)
	}
	g, err := newCandyScanGenerator(dir, false, nil)
	if err != nil || g == nil {
		return nil
	}
	renderGenCache.Store(dir, g)
	return g
}

// renderSeamGenBox loads the cached Generator + the named box for a render-seam method.
// Returns a non-nil errReply (for the caller to return) if either is missing — the shared
// load+guard boilerplate of every box-coupled render-seam method (R3).
func renderSeamGenBox(dir, boxName, method string) (gen *Generator, img *spec.ResolvedBox, errReply *spec.RenderSeamReply) {
	gen = loadRenderGen(dir)
	if gen == nil {
		return nil, nil, &spec.RenderSeamReply{Error: fmt.Sprintf("render-seam %s: no generator for dir %q", method, dir)}
	}
	img = gen.Boxes[boxName]
	if img == nil {
		return nil, nil, &spec.RenderSeamReply{Error: fmt.Sprintf("render-seam %s: box %q not found", method, boxName)}
	}
	return gen, img, nil
}

// renderSeamResult marshals a result struct into a RenderSeamReply (the success path).
func renderSeamResult(method string, result any) (spec.RenderSeamReply, error) {
	out, err := marshalJSON(result)
	if err != nil {
		return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: marshal result: %w", method, err)
	}
	return spec.RenderSeamReply{Result: out}, nil
}

// hostBuildRenderSeam is the "render-seam" host-builder: it dispatches a render-seam request
// by Method to the corresponding core function. A per-method dispatch error (unknown method,
// decode failure, missing gen/box) is a host-side contract bug → returned as a Go error. A core
// function failure is surfaced in reply.Error (the EXACT core error string, so plugin-build
// re-emits it byte-identical to the pre-move core render).
//
//nolint:gocyclo // by-Method dispatch switch — one case per render seam (the 2 remaining loader-gated host-coupled seams); splitting each into a method scatters a single dispatch without reducing the real branch surface.
func hostBuildRenderSeam(_ context.Context, req spec.RenderSeamRequest, _ buildEngineContext) (spec.RenderSeamReply, error) {
	switch req.Method {
	case deploykit.RenderSeamInlineBuilder:
		var p deploykit.InlineBuilderParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: decode params: %w", req.Method, err)
		}
		gen, img, er := renderSeamGenBox(p.Dir, p.BoxName, req.Method)
		if er != nil {
			return *er, nil
		}
		frag, err := gen.resolveInlineBuilderSeam(p.CandyName, p.BuilderName, p.BDef, p.Ctx, img)
		if err != nil {
			return spec.RenderSeamReply{Error: err.Error()}, nil
		}
		return renderSeamResult(req.Method, deploykit.InlineBuilderResult{Fragment: frag})

	case deploykit.RenderSeamEnsureBuilders:
		var p deploykit.EnsureBuildersParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: decode params: %w", req.Method, err)
		}
		gen := loadRenderGen(p.Dir)
		if gen == nil {
			return spec.RenderSeamReply{Error: fmt.Sprintf("render-seam %s: no generator for dir %q", req.Method, p.Dir)}, nil
		}
		if err := ensureBuildersConnected(context.Background(), gen.Config, gen.Dir, p.Words); err != nil {
			return spec.RenderSeamReply{Error: err.Error()}, nil
		}
		return spec.RenderSeamReply{Result: []byte("{}")}, nil
	}

	return spec.RenderSeamReply{}, fmt.Errorf("render-seam: unknown method %q", req.Method)
}

// Register the render-seam host-builder at package-var init (before any init(), like the
// other host-builders). "render-seam" is a CLASS-GENERIC action noun (never a provider word —
// the F11 uniform-API gate).
var _ = func() bool {
	registerHostBuilder("render-seam", typedHostBuilder("render-seam", hostBuildRenderSeam))
	return true
}()
