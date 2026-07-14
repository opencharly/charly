package build

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// render.go — the plugin-build RENDER DRIVE (#67 render-DRIVE move). plugin-build builds a
// deploykit.Generator from the resolved-project envelope (returned by HostBuild("build-prep"))
// + wires the host-coupled seams, then runs dg.Generate(order) to render Containerfiles.
// The render reads RESOLVED data (caches on ResolvedBox + CandyModel) WITHOUT the live
// *Candy/*Config graph — the host build-prep seam filled the caches + projected the envelope.

// buildRenderGenerator constructs a deploykit.Generator from the resolved-project envelope +
// wires the host-coupled seams (the host callbacks the render needs). It returns the Generator
// + the build order (from the reply). The seams that call back to the host use HostBuild /
// InvokeProvider over the in-proc reverse channel (placement-invisible: compiled-in goes
// in-proc, out-of-process goes over gRPC).
func buildRenderGenerator(ctx context.Context, ex *sdk.Executor, rp *spec.ResolvedProject, dir string, devLocalPkg bool) (*deploykit.Generator, error) {
	if rp == nil {
		return nil, fmt.Errorf("render: no resolved-project envelope")
	}

	dg := deploykit.NewRenderGenerator()
	dg.Dir = dir
	dg.Tag = "" // tag is not needed for render (labels use EffectiveVersion)
	dg.BuildDir = filepath.Join(dir, ".build")
	dg.Containerfiles = make(map[string]string)
	dg.GlobalOrder = rp.GlobalOrder
	dg.RequestedBoxes = nil // the order is already filtered by the host
	dg.DevLocalPkg = devLocalPkg

	// Build the CandyModel map from the envelope.
	dg.Candies = make(map[string]deploykit.CandyModel, len(rp.CandyModels))
	for name, cm := range rp.CandyModels {
		cv := rp.Candies[name]
		dg.Candies[name] = deploykit.NewSpecCandyModel(cm, cv)
	}

	// Build the Boxes map from the envelope (re-attach build-render caches).
	dg.Boxes = make(map[string]*deploykit.ResolvedBox, len(rp.Boxes))
	for name, v := range rp.Boxes {
		dg.Boxes[name] = deploykit.NewSpecResolvedBox(v, rp.Distro, rp.Builder)
	}

	// --- wire the host-coupled seams ---

	// renderSeam dispatches one host-coupled render seam to the host via HostBuild("render-seam")
	// (#67). params is the per-method deploykit param struct; out is the per-method result struct
	// to decode into (nil for void methods). The host calls the corresponding CORE function
	// (byte-parity by construction) and returns its error string verbatim in reply.Error.
	renderSeam := func(method string, params any, out any) error {
		pj, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("render-seam %s: marshal params: %w", method, err)
		}
		reqJSON, err := json.Marshal(spec.RenderSeamRequest{Method: method, Params: pj})
		if err != nil {
			return fmt.Errorf("render-seam %s: marshal request: %w", method, err)
		}
		replyJSON, err := ex.HostBuild(ctx, "render-seam", reqJSON)
		if err != nil {
			return fmt.Errorf("render-seam %s: %w", method, err)
		}
		var reply spec.RenderSeamReply
		if err := json.Unmarshal(replyJSON, &reply); err != nil {
			return fmt.Errorf("render-seam %s: decode reply: %w", method, err)
		}
		if reply.Error != "" {
			// The host's exact core-function error string — re-emitted byte-identical.
			return fmt.Errorf("%s", reply.Error)
		}
		if out != nil && len(reply.Result) > 0 {
			if err := json.Unmarshal(reply.Result, out); err != nil {
				return fmt.Errorf("render-seam %s: decode result: %w", method, err)
			}
		}
		return nil
	}

	// EmitPluginOp: dispatch a plugin verb through the host Provider registry (the host
	// checks ProvisionActor act-shell vs OpEmit fragment — logic that stays host-side).
	dg.EmitPluginOp = func(op *spec.Op, img *deploykit.ResolvedBox) (string, bool, error) {
		var res deploykit.EmitPluginOpResult
		if err := renderSeam(deploykit.RenderSeamEmitPluginOp, deploykit.EmitPluginOpParams{Dir: dir, BoxName: img.Name, Op: op}, &res); err != nil {
			return "", false, err
		}
		return res.Out, res.IsScript, nil
	}

	// CollectBoxPorts: from the envelope view (pre-computed by the host projector).
	dg.CollectBoxPorts = func(boxName string) ([]string, error) {
		if v, ok := rp.Boxes[boxName]; ok {
			return v.Ports, nil
		}
		return nil, nil
	}

	// CollectBoxVolume: from the envelope view (pre-computed by the host projector).
	dg.CollectBoxVolume = func(boxName, home string) ([]deploykit.VolumeMount, error) {
		if v, ok := rp.Boxes[boxName]; ok {
			result := make([]deploykit.VolumeMount, len(v.Volumes))
			for i := range v.Volumes {
				result[i] = deploykit.VolumeMount{
					VolumeName:    v.Volumes[i].VolumeName,
					ContainerPath: v.Volumes[i].ContainerPath,
				}
			}
			return result, nil
		}
		return nil, nil
	}

	// ValidateEgress: the host runs the egress CUE validation (bytes mode — traefik routes, …).
	dg.ValidateEgress = func(kind, label string, data []byte) error {
		return renderSeam(deploykit.RenderSeamValidateEgress, deploykit.ValidateEgressParams{Kind: kind, Mode: "bytes", Label: label, Data: data}, nil)
	}

	// ValidateTextEgress: the host gates the rendered Containerfile text (rendered_text/text).
	dg.ValidateTextEgress = func(label, text string) error {
		return renderSeam(deploykit.RenderSeamValidateEgress, deploykit.ValidateEgressParams{Kind: "rendered_text", Mode: "text", Label: label, Data: []byte(text)}, nil)
	}

	// RenderService: the host materializes a ServiceEntry via candy/plugin-init (OpResolve) +
	// egress-gates it (the core RenderService func, byte-exact).
	dg.RenderService = func(entry *spec.ServiceEntry, def *spec.ResolvedInit, ctx spec.ServiceRenderContext) (*spec.RenderedService, error) {
		var res deploykit.RenderServiceResult
		if err := renderSeam(deploykit.RenderSeamRenderService, deploykit.RenderServiceParams{Dir: dir, Entry: entry, Def: def, Ctx: ctx}, &res); err != nil {
			return nil, err
		}
		return res.Rendered, nil
	}

	// ExternalizedBuilders: from the envelope (the registry D-FACT).
	dg.ExternalizedBuilders = rp.ExternalizedBuilders

	// RewriteHeaderCopyForRemote: host-fs materialization (the core gen.rewriteHeaderCopyForRemote).
	dg.RewriteHeaderCopyForRemote = func(headerCopy string) (string, error) {
		var res deploykit.RewriteHeaderCopyResult
		if err := renderSeam(deploykit.RenderSeamRewriteHeaderCopy, deploykit.RewriteHeaderCopyParams{Dir: dir, HeaderCopy: headerCopy}, &res); err != nil {
			return "", err
		}
		return res.HeaderCopy, nil
	}

	// RenderLocalPkgImageInstall: the host rebuilds the LocalPkgInstallStep from the live
	// *Candy graph (the SAME CompileLocalPkgStep origin/main used) + renders it (byte-exact).
	dg.RenderLocalPkgImageInstall = func(step *deploykit.LocalPkgInstallStep, devLocalPkg bool, imageDir, boxName string) (string, error) {
		var res deploykit.LocalPkgResult
		if err := renderSeam(deploykit.RenderSeamLocalPkg, deploykit.LocalPkgParams{Dir: dir, BoxName: boxName, CandyName: step.CandyName, ImageDir: imageDir, DevLocalPkg: devLocalPkg}, &res); err != nil {
			return "", err
		}
		return res.Fragment, nil
	}

	// ResolveInlineBuilder: the host connects + OpResolves an externalized INLINE builder
	// (the core gen.resolveInlineBuilderSeam, byte-exact).
	dg.ResolveInlineBuilder = func(candyName, builderName string, bDef *buildkit.BuilderDef, ctx2 *spec.BuildStageContext, img *deploykit.ResolvedBox) (string, error) {
		var res deploykit.InlineBuilderResult
		if err := renderSeam(deploykit.RenderSeamInlineBuilder, deploykit.InlineBuilderParams{Dir: dir, BoxName: img.Name, CandyName: candyName, BuilderName: builderName, BDef: bDef, Ctx: ctx2}, &res); err != nil {
			return "", err
		}
		return res.Fragment, nil
	}

	// EnsureBuildersConnected: the host connects the externalized detection-builder plugins
	// (the core ensureBuildersConnected, byte-exact).
	dg.EnsureBuildersConnected = func(detected []string) error {
		return renderSeam(deploykit.RenderSeamEnsureBuilders, deploykit.EnsureBuildersParams{Dir: dir, Words: detected}, nil)
	}

	// ResolveDetectionBuilderStage: the host resolves + OpResolves a detection builder
	// (the core gen.resolveDetectionBuilderStageSeam, byte-exact).
	dg.ResolveDetectionBuilderStage = func(builderName string, in spec.BuilderResolveInput, img *deploykit.ResolvedBox) (spec.BuilderResolveReply, error) {
		var res deploykit.DetectionBuilderResult
		if err := renderSeam(deploykit.RenderSeamDetectionBuilder, deploykit.DetectionBuilderParams{Dir: dir, BoxName: img.Name, BuilderName: builderName, In: in}, &res); err != nil {
			return spec.BuilderResolveReply{}, err
		}
		return res.Reply, nil
	}

	// ResolveExternalBuilderStage: the host resolves + OpResolves an external_builder-selected
	// out-of-tree builder (the core gen.resolveExternalBuilderStageSeam, byte-exact).
	dg.ResolveExternalBuilderStage = func(word, candyName string, img *deploykit.ResolvedBox) (spec.BuilderResolveReply, error) {
		var res deploykit.ExternalBuilderResult
		if err := renderSeam(deploykit.RenderSeamExternalBuilder, deploykit.ExternalBuilderParams{Dir: dir, BoxName: img.Name, Word: word, CandyName: candyName}, &res); err != nil {
			return spec.BuilderResolveReply{}, err
		}
		return res.Reply, nil
	}

	// EmitBakedPlugins: via HostBuild("bake-plugins") — the host builds + stages
	// plugin binaries + returns the COPY/chmod fragment.
	dg.EmitBakedPlugins = func(b *strings.Builder, boxName string, candyOrder []string) error {
		reqJSON, err := json.Marshal(spec.BakePluginsRequest{
			Dir:        dir,
			BoxName:    boxName,
			CandyOrder: candyOrder,
		})
		if err != nil {
			return fmt.Errorf("bake-plugins: marshal request: %w", err)
		}
		replyJSON, err := ex.HostBuild(ctx, "bake-plugins", reqJSON)
		if err != nil {
			return fmt.Errorf("bake-plugins: %w", err)
		}
		var reply spec.BakePluginsReply
		if err := json.Unmarshal(replyJSON, &reply); err != nil {
			return fmt.Errorf("bake-plugins: decode reply: %w", err)
		}
		if reply.Error != "" {
			return fmt.Errorf("bake-plugins: %s", reply.Error)
		}
		b.WriteString(reply.Fragment)
		return nil
	}

	return dg, nil
}

// renderContainerfiles builds the deploykit.Generator from the envelope + runs Generate,
// returning the rendered Containerfile content per box name. Called by runBoxGenerate
// (generate-only) and runBoxBuild (build).
func renderContainerfiles(ctx context.Context, ex *sdk.Executor, reply spec.BuildResolveReply, dir string, devLocalPkg bool) (map[string]string, error) {
	dg, err := buildRenderGenerator(ctx, ex, reply.ResolvedProject, dir, devLocalPkg)
	if err != nil {
		return nil, err
	}

	// Determine the render order: filtered (reply.Order) or full (flattened levels).
	var order []string
	if len(reply.Order) > 0 {
		order = reply.Order
	} else {
		for _, level := range reply.Levels {
			order = append(order, level...)
		}
	}

	if err := dg.Generate(order); err != nil {
		return nil, fmt.Errorf("rendering Containerfiles: %w", err)
	}

	return dg.Containerfiles, nil
}