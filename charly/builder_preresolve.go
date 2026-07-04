package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// builder_preresolve.go — the host-side build PRE-PASS that keeps BuildDeployPlan PURE.
//
// An externalized detection-builder's per-candy stage context + teardown ops live in its
// out-of-process plugin (OpCollectContext / OpReverse). collectBuilderContext + BuilderStep.Reverse
// used to type-assert an in-proc BuilderProvider, but BuildDeployPlan is a documented-pure compiler
// (no I/O, no RPC) and BuilderStep.Reverse() runs host-side with no registry handle. So — exactly
// like deploy_preresolve.go for external deploy substrates — the host resolves the
// builder-specific payload BEFORE the pure compile and stashes it on HostContext.BuilderContext;
// the pure compiler reads pre-populated data and NEVER dials a plugin.
//
// CONNECTION IS PRECISELY SCOPED + ON-DEMAND. The pre-pass detects exactly the builders the
// deploy's RESOLVED closure triggers — applying the SAME distro/build-format gate the generator's
// candyNeedsBuilder applies, so a fedora deploy never connects aur (even when a multi-distro candy
// carries an aur: section for its arch variant), and a pixi-only deploy connects ONLY pixi. It then
// build-connects just those plugins by their canonical ref (the same on-demand, scoped pattern as
// connectPluginByWordRef, R3) — NOT a blanket "all four builder plugins" surfaced across an entire
// box scan. A pure pod deploy (no add_candy) never reaches BuildDeployPlan, so it connects nothing.

// builderPreresolved is one candy×builder's pre-resolved payload: the plugin's stage-context keys
// (merged onto the base context by collectBuilderContext) + its teardown ops (stashed onto
// BuilderStep.PreResolvedReverse so Reverse() is a pure getter).
type builderPreresolved struct {
	Context map[string]any
	Reverse []ReverseOp
}

// builderCtxKey keys the pre-resolved map by candy name + builder word (NUL-joined — neither can
// contain NUL, so the key is unambiguous).
func builderCtxKey(candy, builder string) string { return candy + "\x00" + builder }

// detectExternalizedBuilders returns the EXTERNALIZED builder words the deploy's resolved candy
// closure actually triggers, in deterministic order. It applies the SAME detection — INCLUDING the
// distro/build-format gate the generator's candyNeedsBuilder applies — so a DetectConfig builder
// (aur, tightly coupled to a distro's install_template) is surfaced ONLY when the image's build
// formats include that format. Net: a fedora deploy never surfaces aur, a pixi-only deploy surfaces
// ONLY pixi. Pure (no I/O); the unit gate for the scoping fix (builder_preresolve_test.go).
func detectExternalizedBuilders(order []string, layers map[string]*Candy, img *ResolvedBox) []string {
	if img == nil || img.BuilderConfig == nil {
		return nil
	}
	var out []string
	for _, bName := range img.BuilderConfig.BuilderNames() {
		if !externalizedBuilders[bName] {
			continue
		}
		bDef := img.BuilderConfig.Builder[bName]
		if bDef == nil {
			continue
		}
		// Distro gate (mirror generate.go candyNeedsBuilder): a config-only detection (aur) runs a
		// distro-specific install_template, so it is only needed when the image's build formats
		// include that format — a multi-distro candy's aur: section must NOT pull arch tooling onto
		// a fedora build.
		if bDef.DetectConfig != "" && !buildFormatsInclude(img.BuildFormats, bDef.DetectConfig) {
			continue
		}
		for _, candyName := range order {
			if layer := layers[candyName]; layer != nil && candyNeedsBuilderStep(layer, bDef) {
				out = append(out, bName)
				break
			}
		}
	}
	return out
}

// preresolveBuilderContexts connects the EXACT externalized builder plugins the deploy triggers
// (on-demand, scoped) and RPCs each one's OpCollectContext + OpReverse for every (candy, builder)
// in the deploy set, BEFORE the pure BuildDeployPlan compile. Returns the map
// HostContext.BuilderContext carries. A custom candy builder (a builder: vocabulary def with no
// externalized plugin) is skipped — collectBuilderContext gives it base-only context. An
// externalized builder that cannot be connected fails LOUDLY (R4 — never a silent incomplete
// teardown).
func preresolveBuilderContexts(ctx context.Context, cfg *Config, dir string, order []string, layers map[string]*Candy, img *ResolvedBox) (map[string]builderPreresolved, error) {
	needed := detectExternalizedBuilders(order, layers, img)
	if len(needed) == 0 {
		return nil, nil
	}
	if err := ensureBuildersConnected(ctx, cfg, dir, needed); err != nil {
		return nil, err
	}

	var out map[string]builderPreresolved
	for _, candyName := range order {
		layer := layers[candyName]
		if layer == nil {
			continue
		}
		for _, bName := range needed {
			bDef := img.BuilderConfig.Builder[bName]
			if bDef == nil || !candyNeedsBuilderStep(layer, bDef) {
				continue
			}
			prov, ok := providerRegistry.ResolveBuilder(bName)
			if !ok {
				return nil, fmt.Errorf("candy %q: builder %q is externalized but its plugin is not connected (a plugin-load gap?)", candyName, bName)
			}
			collected, err := invokeBuilderCollect(ctx, prov, bName, layer, bDef, img)
			if err != nil {
				return nil, fmt.Errorf("candy %q: builder %q collect-context: %w", candyName, bName, err)
			}
			reverse, err := invokeBuilderReverse(ctx, prov, bName, layer.Name, collected)
			if err != nil {
				return nil, fmt.Errorf("candy %q: builder %q reverse: %w", candyName, bName, err)
			}
			if out == nil {
				out = map[string]builderPreresolved{}
			}
			out[builderCtxKey(layer.Name, bName)] = builderPreresolved{Context: collected, Reverse: reverse}
		}
	}
	return out, nil
}

// ensureBuildersConnected build-connects ONLY the not-yet-connected externalized builder plugins in
// `words`, scoped to those words — the same on-demand, scoped pattern as connectPluginByWordRef
// (R3). It scans the project's OWN candy closure first (main repo: candy/plugin-builder-<word> is a
// local candy/ dir — network-free), falling back to pulling in the SPECIFIC builder plugins by
// their canonical ref (a box/<distro> submodule deploy that triggers a builder but vendors the
// plugin nowhere; under CHARLY_REPO_OVERRIDE the ref resolves to the local superproject). A builder
// whose plugin still will not connect is a LOUD error (R4).
func ensureBuildersConnected(ctx context.Context, cfg *Config, dir string, words []string) error {
	refs := map[string]struct{}{}
	var extraRefs []string
	for _, w := range words {
		if _, ok := providerRegistry.ResolveBuilder(w); ok {
			continue // already connected this process
		}
		refs[w] = struct{}{}
		if ref, ok := externalBuilderPluginRef(w); ok {
			extraRefs = append(extraRefs, ref)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	if cfg == nil {
		return fmt.Errorf("builder plugin connect: no project config (cannot scan %v)", words)
	}
	for _, opts := range []ResolveOpts{{}, {ExtraCandyRefs: extraRefs}} {
		candyMap, scanErr := ScanAllCandyWithConfigOpts(dir, cfg, opts)
		if scanErr != nil || candyMap == nil {
			continue
		}
		if perr := loadProjectPlugins(ctx, candyMap, refs); perr != nil {
			return fmt.Errorf("builder plugin load %v: %w", words, perr)
		}
		allConnected := true
		for w := range refs {
			if _, ok := providerRegistry.ResolveBuilder(w); !ok {
				allConnected = false
				break
			}
		}
		if allConnected {
			return nil
		}
	}
	var missing []string
	for w := range refs {
		if _, ok := providerRegistry.ResolveBuilder(w); !ok {
			missing = append(missing, w)
		}
	}
	return fmt.Errorf("externalized builder plugin(s) %v could not be connected (plugin candy not found / build failed)", missing)
}

// invokeBuilderCollect Invokes the builder plugin's OpCollectContext, returning the
// builder-specific stage-context keys. The host fills the generic descriptor it can derive
// without builder-specific knowledge: the candy/builder/home always, plus the builder's
// detect-config package section (Packages/Replaces) used today only by aur.
func invokeBuilderCollect(ctx context.Context, prov Provider, word string, layer *Candy, bDef *BuilderDef, img *ResolvedBox) (map[string]any, error) {
	in := spec.BuilderCollectInput{Candy: layer.Name, Builder: word, Home: img.Home}
	if bDef.DetectConfig != "" {
		if sec := layer.FormatSection(bDef.DetectConfig); sec != nil {
			in.Packages = append([]string(nil), sec.Packages...)
			if raw, ok := sec.Raw["replaces"]; ok {
				if list, ok := stringSliceFromYAML(raw); ok {
					in.Replaces = list
				}
			}
		}
	}
	params, err := marshalJSON(in)
	if err != nil {
		return nil, fmt.Errorf("marshal collect-context input: %w", err)
	}
	res, err := prov.Invoke(ctx, &Operation{Reserved: word, Op: OpCollectContext, Params: params})
	if err != nil {
		return nil, err
	}
	var reply spec.BuilderCollectReply
	if err := json.Unmarshal(res.JSON, &reply); err != nil {
		return nil, fmt.Errorf("decode collect-context reply: %w", err)
	}
	return reply.Context, nil
}

// invokeBuilderReverse Invokes the builder plugin's OpReverse with the resolved stage context,
// returning the teardown ops the host stashes on BuilderStep.PreResolvedReverse.
func invokeBuilderReverse(ctx context.Context, prov Provider, word, candy string, stageContext map[string]any) ([]ReverseOp, error) {
	params, err := marshalJSON(spec.BuilderReverseInput{Candy: candy, Builder: word, Context: stageContext})
	if err != nil {
		return nil, fmt.Errorf("marshal reverse input: %w", err)
	}
	res, err := prov.Invoke(ctx, &Operation{Reserved: word, Op: OpReverse, Params: params})
	if err != nil {
		return nil, err
	}
	var reply spec.BuilderReverseReply
	if err := json.Unmarshal(res.JSON, &reply); err != nil {
		return nil, fmt.Errorf("decode reverse reply: %w", err)
	}
	return reply.ReverseOps, nil
}
