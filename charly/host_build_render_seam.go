package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// host_build_render_seam.go — the "render-seam" host-builder (#67 render-DRIVE move).
// plugin-build's deploykit.Generator render calls back to the host for each host-coupled seam
// (RenderService, the builder resolves, ValidateEgress, EmitPluginOp, localpkg, header-copy,
// ensure-builders) via HostBuild("render-seam", RenderSeamRequest{Method, Params}). This
// builder dispatches by Method to the corresponding CORE function — the EXACT funcs the core
// toDeploykit closures call — so the render is byte-identical to the pre-move core render
// (byte-parity by construction). The rich inputs (spec types — BuilderResolveInput, ServiceEntry,
// ResolvedInit, ServiceRenderContext, Builder, BuildStageContext, Op) ride the opaque Params
// bytes; the host unmarshals + calls. The live *Generator (gen.Boxes/gen.Candies/gen.Config/
// gen.Dir) comes from the per-dir renderGenCache populated by build-prep (one gen per dir per
// process) — no per-call reload.

// renderGenCache holds the live *Generator per project dir for the render-seam host-builder.
// Populated by hostBuildBuildResolve (the first HostBuild in a box build/generate); read by
// hostBuildRenderSeam. One entry per dir per process — a single `charly box build` is one
// process, so the cache holds the one gen build-prep loaded (render-prep already run).
var renderGenCache sync.Map

// loadRenderGen returns the cached *Generator for dir, falling back to a fresh NewGenerator
// (default opts) if the cache is empty (defensive — build-prep always populates it first).
func loadRenderGen(dir string) *Generator {
	if v, ok := renderGenCache.Load(dir); ok {
		return v.(*Generator)
	}
	g, err := NewGenerator(dir, "", ResolveOpts{})
	if err != nil || g == nil {
		return nil
	}
	renderGenCache.Store(dir, g)
	return g
}

// candyModelByLeaf returns the deploykit CandyModel for the candy whose LEAF name (Candy.Name)
// matches leaf, or nil. The render-seam carries the candy LEAF (step.CandyName); the live
// gen.Candies is keyed by the full scanned-set key (a remote ref), so the lookup is by leaf —
// the stable identity the deploykit render (candyOrder) and the live graph share.
func candyModelByLeaf(candies map[string]*Candy, leaf string) deploykit.CandyModel {
	cm := candyModelMap(candies)
	for k, c := range candies {
		if c != nil && c.Name == leaf {
			return cm[k]
		}
	}
	return nil
}

// renderSeamGenBox loads the cached Generator + the named box for a render-seam method.
// Returns a non-nil errReply (for the caller to return) if either is missing — the shared
// load+guard boilerplate of every box-coupled render-seam method (R3).
func renderSeamGenBox(dir, boxName, method string) (gen *Generator, img *buildkit.ResolvedBox, errReply *spec.RenderSeamReply) {
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
//nolint:gocyclo // by-Method dispatch switch — one case per render seam (the 9 host-coupled seams); splitting each into a method scatters a single dispatch without reducing the real branch surface.
func hostBuildRenderSeam(_ context.Context, req spec.RenderSeamRequest, _ buildEngineContext) (spec.RenderSeamReply, error) {
	switch req.Method {
	case deploykit.RenderSeamRenderService:
		var p deploykit.RenderServiceParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: decode params: %w", req.Method, err)
		}
		rendered, err := RenderService(p.Entry, p.Def, p.Ctx)
		if err != nil {
			return spec.RenderSeamReply{Error: err.Error()}, nil
		}
		out, err := marshalJSON(deploykit.RenderServiceResult{Rendered: rendered})
		if err != nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: marshal result: %w", req.Method, err)
		}
		return spec.RenderSeamReply{Result: out}, nil

	case deploykit.RenderSeamDetectionBuilder:
		var p deploykit.DetectionBuilderParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: decode params: %w", req.Method, err)
		}
		gen, img, er := renderSeamGenBox(p.Dir, p.BoxName, req.Method)
		if er != nil {
			return *er, nil
		}
		reply, err := gen.resolveDetectionBuilderStageSeam(p.BuilderName, p.In, img)
		if err != nil {
			return spec.RenderSeamReply{Error: err.Error()}, nil
		}
		return renderSeamResult(req.Method, deploykit.DetectionBuilderResult{Reply: reply})

	case deploykit.RenderSeamExternalBuilder:
		var p deploykit.ExternalBuilderParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: decode params: %w", req.Method, err)
		}
		gen, img, er := renderSeamGenBox(p.Dir, p.BoxName, req.Method)
		if er != nil {
			return *er, nil
		}
		reply, err := gen.resolveExternalBuilderStageSeam(p.Word, p.CandyName, img)
		if err != nil {
			return spec.RenderSeamReply{Error: err.Error()}, nil
		}
		return renderSeamResult(req.Method, deploykit.ExternalBuilderResult{Reply: reply})

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

	case deploykit.RenderSeamLocalPkg:
		var p deploykit.LocalPkgParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: decode params: %w", req.Method, err)
		}
		gen, img, er := renderSeamGenBox(p.Dir, p.BoxName, req.Method)
		if er != nil {
			return *er, nil
		}
		// Rebuild the LocalPkgInstallStep from the SAME source origin/main used
		// (candyModelMap(gen.Candies) — the toDeploykit conversion), so byte-exact. The
		// deploykit seam carries step.CandyName (the candy LEAF name), but gen.Candies is keyed
		// by the full scanned-set key (a remote candy's ref, e.g. github.com/.../charly), so
		// look the candy up by LEAF name (origin/main had the step pre-built from the keyed
		// candy; the leaf is the stable identity both halves share).
		cm := candyModelByLeaf(gen.Candies, p.CandyName)
		if cm == nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: candy %q not found", req.Method, p.CandyName)
		}
		step := deploykit.CompileLocalPkgStep(cm, img, deploykit.HostContext{})
		if step == nil {
			return spec.RenderSeamReply{Result: []byte("{}")}, nil
		}
		s, ok := step.(*deploykit.LocalPkgInstallStep)
		if !ok {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: CompileLocalPkgStep returned %T, not *LocalPkgInstallStep", req.Method, step)
		}
		frag, err := renderLocalPkgImageInstall(s, p.DevLocalPkg, p.ImageDir, p.BoxName)
		if err != nil {
			return spec.RenderSeamReply{Error: err.Error()}, nil
		}
		out, err := marshalJSON(deploykit.LocalPkgResult{Fragment: frag})
		if err != nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: marshal result: %w", req.Method, err)
		}
		return spec.RenderSeamReply{Result: out}, nil

	case deploykit.RenderSeamRewriteHeaderCopy:
		var p deploykit.RewriteHeaderCopyParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: decode params: %w", req.Method, err)
		}
		gen := loadRenderGen(p.Dir)
		if gen == nil {
			return spec.RenderSeamReply{Error: fmt.Sprintf("render-seam %s: no generator for dir %q", req.Method, p.Dir)}, nil
		}
		hc, err := gen.rewriteHeaderCopyForRemote(p.HeaderCopy)
		if err != nil {
			return spec.RenderSeamReply{Error: err.Error()}, nil
		}
		out, err := marshalJSON(deploykit.RewriteHeaderCopyResult{HeaderCopy: hc})
		if err != nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: marshal result: %w", req.Method, err)
		}
		return spec.RenderSeamReply{Result: out}, nil

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

	case deploykit.RenderSeamEmitPluginOp:
		var p deploykit.EmitPluginOpParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: decode params: %w", req.Method, err)
		}
		if p.Op == nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: nil op", req.Method)
		}
		_, img, er := renderSeamGenBox(p.Dir, p.BoxName, req.Method)
		if er != nil {
			return *er, nil
		}
		// The exact core EmitPluginOp closure (toDeploykit): ResolveVerb → ProvisionActor
		// act-shell, else emitPluginFragment OpEmit.
		prov, ok := providerRegistry.ResolveVerb(p.Op.Plugin)
		if !ok {
			return spec.RenderSeamReply{Error: fmt.Sprintf("run: plugin verb %q is not registered (an external plugin not connected at build time?)", p.Op.Plugin)}, nil
		}
		if actor, isActor := prov.(ProvisionActor); isActor {
			script, sok := actor.RenderProvisionScript(p.Op, img.Tags)
			if !sok {
				return spec.RenderSeamReply{Error: fmt.Sprintf("run: plugin verb %q is not act-capable (ProvisionActor declined)", p.Op.Plugin)}, nil
			}
			return renderSeamResult(req.Method, deploykit.EmitPluginOpResult{Out: script, IsScript: true})
		}
		frag, ferr := emitPluginFragment(prov, p.Op, img)
		if ferr != nil {
			return spec.RenderSeamReply{Error: fmt.Sprintf("run: plugin verb %q build-emit: %s", p.Op.Plugin, ferr.Error())}, nil
		}
		return renderSeamResult(req.Method, deploykit.EmitPluginOpResult{Out: frag, IsScript: false})

	case deploykit.RenderSeamValidateEgress:
		var p deploykit.ValidateEgressParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return spec.RenderSeamReply{}, fmt.Errorf("render-seam %s: decode params: %w", req.Method, err)
		}
		if err := egressValidate(p.Kind, p.Label, p.Mode, string(p.Data)); err != nil {
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
