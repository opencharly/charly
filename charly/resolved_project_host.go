package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// resolved_project_host.go — the THIN host FILL for the RESOLVED-project envelope (K5-Unit-0, the
// S-K5 keystone). It is a DATA PROJECTION over the resolve engines that already exist — ResolveBox
// (per enabled box) + ScanAllCandy + the folded uf.Bundle deploy tree — serialized into the generic
// spec.ResolvedProject (spec.#ResolvedProject, the third member of the envelope spine after
// spec.ParsedProject → spec.LoadedProject). It is NOT a new engine: it copies fields the existing
// engines populate, dropping the 6 host-only json:"-" compute-cache pointers of ResolvedBox that are
// never wire data (DistroConfig/DistroDef/BuilderConfig/InitSystem/InitDef/CandyCaps).
//
// It is exposed BOTH ways: as the generic action-noun HostBuild kind "resolved-project" (F11 — a
// plugin requests it over the established reverse channel via Executor.HostBuild) AND callable
// host-side directly (projectResolvedBox / projectCandyView / buildResolvedProjectFromDir). The
// per-concern config-resolve / status-substrate seams collapse into this one envelope at their
// consumers' later K5 units (the AI-harness check projection already did — plugin-check reads it here).

// resolvedProjectBuilderKind is the F11 hostBuilders key — a generic action noun, never a provider word.
const resolvedProjectBuilderKind = "resolved-project"

// projectResolvedBox projects a resolved box (ResolvedBox = buildkit.ResolvedBox) into the wire-safe
// spec.ResolvedBoxView: EXACTLY the non-json:"-" fields `charly box inspect` already serializes
// (json.MarshalIndent(*ResolvedBox)), in declaration order. The 6 json:"-" host-only compute caches
// are DROPPED — they are re-derivable by a resolving plugin (or reached via RunHostStep), never wire
// data (S-K5 verdict, the design key).
func projectResolvedBox(b *buildkit.ResolvedBox) spec.ResolvedBoxView {
	v := spec.ResolvedBoxView{
		Name:                  b.Name,
		Version:               b.Version,
		EffectiveVersion:      b.EffectiveVersion,
		Status:                b.Status,
		Info:                  b.Info,
		CheckLevel:            b.CheckLevel,
		Base:                  b.Base,
		From:                  b.From,
		BootstrapBuilderImage: b.BootstrapBuilderImage,
		Platforms:             b.Platforms,
		Tag:                   b.Tag,
		Registry:              b.Registry,
		Pkg:                   b.Pkg,
		Distro:                b.Distro,
		BuildFormats:          b.BuildFormats,
		Tags:                  b.Tags,
		Candy:                 b.Candy,
		User:                  b.User,
		UID:                   int64(b.UID),
		GID:                   int64(b.GID),
		Home:                  b.Home,
		UserAdopted:           b.UserAdopted,
		Merge:                 b.Merge,
		Builder:               map[string]string(b.Builder),
		BuilderCapabilities:   b.BuilderCapabilities,
		Auto:                  b.Auto,
		Network:               b.Network,
		DataImage:             b.DataImage,
		IsExternalBase:        b.IsExternalBase,
		FullTag:               b.FullTag,
	}
	// build-RENDER caches (#67): copy when present. Filled ONLY in the build-render
	// projection (render-prep ran); empty for the validate/inspect path.
	v.BakedMetadata = b.BakedMetadata
	v.RenderCandyOrder = b.RenderCandyOrder
	v.InitSystem = b.InitSystem
	v.InitDef = b.InitDef
	v.ActiveInits = b.ActiveInits
	if b.CandyCaps != nil {
		v.Caps = &spec.AggregatedCandyCapsView{
			PreserveUser:       b.CandyCaps.PreserveUser,
			NeedsRootAfterInit: b.CandyCaps.NeedsRootAfterInit,
			OCILabels:          b.CandyCaps.OCILabels,
		}
	}
	return v
}

// rawCandyPair returns the underlying (spec.CandyModel, spec.CandyView) pair a wrapped
// spec.CandyReader carries — the W9 escape hatch (deploykit.specCandyAdapter.RawCandy(), reached
// via a structural type assertion so charly core never imports deploykit for this). Once the
// scan-machinery move (loaderkit.ScanCandyManifest/ScanInlineCandy/ScanRemoteCandy) constructs the
// wire-shaped CandyModel/CandyView DIRECTLY, the raw pair coming back out of a wrapped CandyReader
// already IS ResolvedProject.CandyModels[name] / .Candies[name] verbatim — no re-projection needed
// (the old projectCandyView/projectCandyModel pre-move re-derived these fields from a live *Candy;
// there is no live *Candy anymore to project FROM). ok is false only for a CandyReader implementer
// that isn't NewSpecCandyModel's adapter (no such implementer exists in production; a defensive,
// never-panicking fallback for the theoretical case).
func rawCandyPair(r spec.CandyReader) (spec.CandyModel, spec.CandyView, bool) {
	raw, ok := r.(interface {
		RawCandy() (spec.CandyModel, spec.CandyView)
	})
	if !ok {
		return spec.CandyModel{}, spec.CandyView{}, false
	}
	m, v := raw.RawCandy()
	return m, v, true
}

// projectResolvedProject assembles the spec.ResolvedProject from already-loaded resolve-engine
// outputs — a pure DATA projection, no resolution logic of its own. boxes come from ResolveBox
// (enabled boxes, sorted), candies from ScanAllCandy (sorted), deploy from the folded uf.Bundle tree.
// projectResolvedProject assembles the envelope. When diags is nil it is FAIL-FAST (a per-box
// ResolveBox failure aborts the whole projection with an error — the resolved-project contract
// inspect/list rely on). When diags is non-nil it is ERROR-TOLERANT (the validate-project path): a
// ResolveBox failure appends a spec.Diagnostic and SKIPS that box, so validate runs on a broken
// project. The box-aggregate collectors already tolerate errors (a failed collector leaves that
// aggregate empty), so the tolerant branch is confined to the ResolveBox call.
func projectResolvedProject(cfg *Config, layers map[string]spec.CandyReader, uf *UnifiedFile, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, initCfg *InitConfig, dir, version string, opts ResolveOpts, diags *spec.Diagnostics) (*spec.ResolvedProject, error) {
	return projectResolvedProjectWithBoxes(cfg, layers, uf, distroCfg, builderCfg, initCfg, dir, version, opts, diags, nil)
}

// projectBoxAggregates fills the box-AUTHORED + box-AGGREGATE fields on a ResolvedBoxView
// from the authored BoxConfig + the cross-candy collectors. The authored surfaces (Plan,
// AuthoredAliases) come from cfg.BoxConfig(name) and are SKIPPED for auto-intermediate boxes
// (which have no authored config). The aggregates (Ports/Volumes/Aliases/Engine) read
// cfg+layers by name and work for authored boxes AND intermediates — render-prep's
// buildBakedMetadata already used the same collectors for every gen.Box. A collector error
// leaves that aggregate empty (a read-only projection never fails the whole load). Shared by
// the pre-resolved (build-prep), fresh-resolve (validate), and auto-intermediate passes (R3).
func projectBoxAggregates(cfg *Config, layers map[string]spec.CandyReader, name string, resolved *buildkit.ResolvedBox, view *spec.ResolvedBoxView) {
	if img, ok := cfg.BoxConfig(name); ok {
		view.Plan = img.Plan
		view.AuthoredAliases = img.Alias
		// K5-Unit-1 (#67 keystone): the box-AUTHORED deploy-overlay surfaces ExportAllBox reads
		// off the envelope instead of the live *Config graph. description is the RAW authored
		// string (Info above is its descriptionInfo first-line summary); env/env_file/security
		// are the box-authored deploy-overlay defaults. Filled alongside plan/authored_aliases.
		view.Description = img.Description
		view.Env = img.Env
		view.EnvFile = img.EnvFile
		view.Security = img.Security
	}
	if ports, perr := CollectBoxPorts(cfg, layers, name); perr == nil {
		view.Ports = ports
	}
	if vols, verr := CollectBoxVolume(cfg, layers, name, resolved.Home, nil); verr == nil {
		for _, vm := range vols {
			view.Volumes = append(view.Volumes, spec.ResolvedVolumeMount(vm))
		}
	}
	if als, aerr := CollectBoxAlias(cfg, layers, name); aerr == nil {
		for _, a := range als {
			view.Aliases = append(view.Aliases, spec.CandyAlias(a))
		}
	}
	view.Engine = ResolveBoxEngine(cfg, layers, name, "")
}

// projectResolvedProjectWithBoxes is the envelope assembler with an optional pre-resolved
// boxes map. When preResolvedBoxes is non-nil (the build-prep seam path), the boxes are used
// AS-IS — skipping the cfg.ResolveBox loop — so the render-prep caches
// (BakedMetadata/RenderCandyOrder/InitSystem/InitDef/ActiveInits/CandyCaps) are preserved
// on the ResolvedBoxView. When nil (the validate/inspect path), boxes are resolved fresh.
//
//nolint:gocyclo // envelope assembler — the box loop (pre-resolved vs fresh-resolve vs intermediate) + the candy/deploy/vocab projections; one branch per projection arm.
func projectResolvedProjectWithBoxes(cfg *Config, layers map[string]spec.CandyReader, uf *UnifiedFile, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, initCfg *InitConfig, dir, version string, opts ResolveOpts, diags *spec.Diagnostics, preResolvedBoxes map[string]*buildkit.ResolvedBox) (*spec.ResolvedProject, error) {
	rp := &spec.ResolvedProject{Version: version}

	// R1 fix (K1-unblock wave 2): pre-populate opts.DistroCfg/BuilderCfg from the ALREADY-LOADED
	// values this function received, so ResolveBox's fillBuildConfigFallback guard
	// (`if opts.DistroCfg == nil && opts.BuilderCfg == nil`) short-circuits instead of re-running a
	// full LoadUnified(dir) — the WHOLE multi-repo project walk — on EVERY ResolveBox call. This was
	// a dormant cost: main's own box loop below has iterated ZERO boxes since the 2026-06 box
	// inversion (main owns no boxes), so it never paid this price. fillNamespacedBoxes (below) is
	// the FIRST caller to run ResolveBox in a real loop — confirmed via live timing (this repo's
	// full namespaced box set: 80 boxes × ~750ms reload = 69s to resolve the project once, a severe
	// `charly box validate`/every-resolved-project-caller regression this wave newly exposes).
	// Behavior-IDENTICAL, not a semantic change: fillBuildConfigFallback's reload path resolves the
	// SAME dir every call regardless of which box/namespace is being resolved (it never varied by
	// namespace), so caching the ONE value it always produced is a pure performance fix — verified
	// distro: inherits across a namespace boundary (per CLAUDE.md's own note); this pins the ROOT's
	// distro/builder vocabulary for every namespace's box resolution, matching what the slow
	// per-call reload ALWAYS computed (same dir, same result, every time).
	if opts.DistroCfg == nil {
		opts.DistroCfg = distroCfg
	}
	if opts.BuilderCfg == nil {
		opts.BuilderCfg = builderCfg
	}

	calver := ComputeCalVer()
	resolvedBoxes := map[string]*buildkit.ResolvedBox{}
	for _, name := range cfg.AllBoxNames() {
		img, ok := cfg.BoxConfig(name)
		if !ok {
			continue
		}
		if !img.IsEnabled() && !opts.shouldIncludeDisabled(name) {
			continue
		}
		// When pre-resolved boxes are provided (build-prep seam), use them directly —
		// render-prep has already filled the build-render caches on them.
		if preResolvedBoxes != nil {
			resolved, exists := preResolvedBoxes[name]
			if !exists {
				continue
			}
			resolvedBoxes[name] = resolved
			view := projectResolvedBox(resolved)
			projectBoxAggregates(cfg, layers, name, resolved, &view)
			if rp.Boxes == nil {
				rp.Boxes = make(map[string]spec.ResolvedBoxView, len(cfg.Box))
			}
			rp.Boxes[name] = view
			continue
		}
		resolved, err := ResolveBox(cfg, name, calver, dir, opts)
		if err != nil {
			if diags == nil {
				return nil, fmt.Errorf("resolving box %q: %w", name, err)
			}
			continue
		}
		resolvedBoxes[name] = resolved
		view := projectResolvedBox(resolved)
		projectBoxAggregates(cfg, layers, name, resolved, &view)
		if rp.Boxes == nil {
			rp.Boxes = make(map[string]spec.ResolvedBoxView, len(cfg.Box))
		}
		rp.Boxes[name] = view
	}

	// Auto-intermediates (#67): preResolvedBoxes (gen.Boxes) carries the auto-generated
	// intermediate images that cfg.allBoxNames() (authored-only) omits. The build order
	// returned to plugin-build includes them, so the render envelope must too — otherwise
	// dg.Generate(order) hits a box not in dg.Boxes and panics. The collectors read
	// cfg+layers by name and work for intermediates (render-prep's buildBakedMetadata
	// already used them for every gen.Box); an intermediate has no authored Plan/alias,
	// which projectBoxAggregates skips via the cfg.BoxConfig(name) ok-check. A no-op range
	// when preResolvedBoxes is nil (the validate/inspect path passes nil).
	for name, resolved := range preResolvedBoxes {
		if _, exists := rp.Boxes[name]; exists {
			continue
		}
		resolvedBoxes[name] = resolved
		view := projectResolvedBox(resolved)
		projectBoxAggregates(cfg, layers, name, resolved, &view)
		if rp.Boxes == nil {
			rp.Boxes = make(map[string]spec.ResolvedBoxView, len(preResolvedBoxes))
		}
		rp.Boxes[name] = view
	}

	for name, c := range layers {
		if c == nil {
			continue
		}
		m, v, ok := rawCandyPair(c)
		if !ok {
			continue
		}
		if rp.Candies == nil {
			rp.Candies = make(map[string]spec.CandyView, len(layers))
			rp.CandyModels = make(map[string]spec.CandyModel, len(layers))
		}
		rp.Candies[name] = v
		rp.CandyModels[name] = m
	}

	// K1-unblock wave 2: namespace-qualified box views (`ns.name`), so a consumer resolving a
	// possibly-namespace-qualified box reference (mirroring the ALREADY-established BoxPlans
	// qualified-key pattern below) can do a presence/view check off THIS envelope alone, without
	// LoadUnified access. Purely ADDITIVE (root-scoped rp.Boxes entries are untouched; a qualified
	// key can never collide with a bare name) and BEST-EFFORT — mirrors fillBoxPlans's own
	// tolerance: an unresolvable namespaced box (e.g. one referencing a builder not reachable from
	// THIS project's build context) is skipped, never aborts the whole envelope, even when the
	// root-box loop above runs fail-fast (diags == nil). Runs AFTER the root-scope rp.Candies/
	// rp.CandyModels fill above (R1 fix, same wave: fillNamespacedBoxes ALSO folds in each
	// namespace's OWN candy scan — see its doc comment for why this closes a real
	// `charly box validate` regression the root-only fill left namespaced boxes exposed to).
	if uf != nil {
		fillNamespacedBoxes(uf, "", calver, dir, opts, rp, map[*UnifiedFile]bool{})
	}

	if uf != nil && len(uf.Bundle) > 0 {
		// BundleNode is a type alias for spec.Deploy, so the folded deploy tree projects into the
		// envelope's map[string]*Deploy directly (a per-iteration copy, addressed).
		rp.Deploy = make(map[string]*spec.Deploy, len(uf.Bundle))
		for k, v := range uf.Bundle {
			node := v
			rp.Deploy[k] = &node
		}
	}

	// K1-unblock wave 1: resolved `resource:` kind entities. ResolvedResource is an intra-spec
	// alias (spec.ResolvedResource, resource_resolve.go), so this is a straight assignment — no
	// re-projection needed. The former sole consumer of this data (charly/arbiter_host.go's
	// "resources" HostArbiter seam) is retired in favor of reading it off THIS envelope.
	if uf != nil {
		if resources := uf.resolveResources(); len(resources) > 0 {
			rp.Resources = resources
		}
	}

	// build VOCABULARY (the validate ENGINE consumer): the embedded distro/builder/init sections.
	// DistroDef=spec.ResolvedDistro, BuilderDef=spec.Builder, ResolvedInit=spec.ResolvedInit, so the
	// maps assign straight into the pinned envelope members.
	if distroCfg != nil {
		rp.Distro = distroCfg.Distro
	}
	if builderCfg != nil {
		rp.Builder = builderCfg.Builder
	}
	if initCfg != nil {
		rp.Init = initCfg.Init
	}
	// ExternalizedBuilders (the registry D-FACT: which builder words are served by an external
	// out-of-process plugin) is a fixed constant, not project-derived, so the generic
	// "resolved-project" seam populates it exactly like the "build-prep" (build_resolve_host.go) and
	// "overlay" (build_overlay.go) seams already do — R3 single source, so a resolved-project
	// CONSUMER (candy/plugin-installstep's OWN deploykit.Generator, built from THIS envelope) can
	// dispatch a builder word (externalized vs a project-custom vocabulary builder) without a
	// SEPARATE host round-trip for the fact.
	rp.ExternalizedBuilders = externalizedBuilders

	if uf != nil {
		// kind TEMPLATES (validate localtemplates + check-include pod/vm arms + status k8s/adb).
		rp.Templates = projectTemplates(uf)
		// kind:agent catalog (the harness AI-CLI pick + charly feature list-agent).
		if agents := uf.PluginKinds["agent"]; len(agents) > 0 {
			rp.AgentBodies = make(map[string]spec.RawBody, len(agents))
			for k, v := range agents {
				rp.AgentBodies[k] = v
			}
		}
	}

	// box_plans (the `include: box:<name>` plan-splice arm): the include-ready FLATTENED acceptance
	// plan per box, computed by the SAME base-chain CollectDescriptions the former in-core box arm
	// used (candy-chain bakeable steps + the box-level bakeable plan), keyed by the QUALIFIED box
	// name so a namespaced ref (fedora.jupyter) resolves. A plugin cannot recompute it (base-chain
	// walk + candy-order + bakeable filter are host resolve Mechanisms over the runtime Candy).
	boxPlans := map[string][]spec.Step{}
	fillBoxPlans(cfg, layers, "", boxPlans, map[*Config]bool{})
	if len(boxPlans) > 0 {
		rp.BoxPlans = boxPlans
	}

	// build ORDER + auto-intermediates (charly box list targets): ComputeIntermediates adds the
	// auto-generated intermediate images; ResolveBoxOrder returns them dependency-ordered.
	if inter, ierr := ComputeIntermediates(resolvedBoxes, layers, cfg, calver); ierr == nil {
		if order, oerr := deploykit.ResolveBoxOrder(inter, layers); oerr == nil {
			for _, name := range order {
				bt := spec.BuildTarget{Name: name}
				if b := inter[name]; b != nil {
					bt.Auto = b.Auto
				}
				rp.BuildTargets = append(rp.BuildTargets, bt)
			}
		}
	}

	return rp, nil
}

// fillBoxPlans populates out with the include-ready FLATTENED acceptance plan for every box
// reachable from cfg (its own boxes + every import namespace, recursively), keyed by QUALIFIED
// name (`fedora.jupyter`). It mirrors the former in-core `include: box:<name>` arm EXACTLY: the
// SAME CollectDescriptions base-chain walk (candy-chain bakeable steps + the box-level bakeable
// plan) flattened over the three sections, so the relocated plugin box arm reads a byte-equivalent
// plan without the resolve engine. Only boxes with a non-empty plan are recorded. The visited set
// guards the pointer-keyed namespace cache against a self-referential cycle.
func fillBoxPlans(cfg *Config, layers map[string]spec.CandyReader, prefix string, out map[string][]spec.Step, visited map[*Config]bool) {
	if cfg == nil || visited[cfg] {
		return
	}
	visited[cfg] = true
	for _, name := range cfg.AllBoxNames() {
		qualified := name
		if prefix != "" {
			qualified = prefix + "." + name
		}
		set := deploykit.CollectDescriptions(cfg, layers, name)
		if set == nil {
			continue
		}
		var steps []spec.Step
		for _, sec := range [][]kit.LabeledDescription{set.Candy, set.Box, set.Deploy} {
			for _, ld := range sec {
				steps = append(steps, ld.Plan...)
			}
		}
		if len(steps) > 0 {
			out[qualified] = steps
		}
	}
	for ns, sub := range cfg.Namespaces {
		child := ns
		if prefix != "" {
			child = prefix + "." + ns
		}
		fillBoxPlans(sub, layers, child, out, visited)
	}
}

// fillNamespacedBoxes populates *out with a namespace-QUALIFIED spec.ResolvedBoxView (`fedora.jupyter`,
// or `ns1.ns2.name` for a nested import) for every box reachable from cfg's import namespaces,
// recursively (cfg's OWN boxes are filled by the root loop in projectResolvedProjectWithBoxes — this
// only adds the namespaced ones, matching fillBoxPlans's exact prefix-recursion shape and its SAME
// layers/visited-cycle-guard contract). A namespaced box that fails to resolve (e.g. references a
// builder unreachable from the root project's build context) is SKIPPED, never fatal — this fill is
// best-effort/additive by design, unlike the root-box loop's optional fail-fast (diags == nil) mode,
// because a namespace's own box graph may be only PARTIALLY reachable from THIS project's resolve
// context.
// fillNamespacedBoxes recurses uf.Namespaces (a *UnifiedFile tree — R1 fix, same wave: NOT
// cfg.Namespaces, see below), adding a qualified spec.ResolvedBoxView for every namespaced box to
// rp.Boxes, AND folding that namespace's OWN candy set into rp.Candies/rp.CandyModels —
// bare-ref-keyed, exactly like the root-scope fill above — so a namespaced box's candy dependency
// list (which may reference candies ONLY reachable through that namespace's own discover:/require:
// closure — e.g. a distro submodule's box pinning a shared candy from the parent superproject via
// `@github.com/opencharly/charly/candy/X:vTAG`) resolves against rp.CandyModels the SAME way the
// root project's own boxes do.
//
// Root cause this closes: before this fix, rp.CandyModels came ONLY from the ROOT project's own
// ScanAllCandyWithConfigOpts scan (walking the ROOT's discover:/require: edges) — a namespaced
// box's candy refs, reachable only through ITS OWN sub-config's edges, were never scanned at all.
// That gap was DORMANT (unreachable) before this same wave added namespaced boxes to rp.Boxes in
// the first place — candy/plugin-box's validate rules never iterated a namespaced box, so they
// never tried the lookup and never failed. Making namespaced boxes visible without ALSO making
// their candy closure resolvable left `charly box validate` failing hard ("unknown candy" / "candy
// not found") on every namespaced box, confirmed via a live origin/main-vs-this-branch comparison
// (zero such errors on origin/main with the identical box submodules checked out).
//
// Why *UnifiedFile, not *Config (an R1 correction to this fix's OWN first draft): a first attempt
// called ScanAllCandyWithConfigOpts(dir, sub, opts) — but that function's LOCAL-candy discovery
// (scanLocalCandies) re-invokes LoadUnified(dir) FRESH, ignoring its cfg parameter entirely, so it
// just redundantly re-scanned the ROOT project and never found a namespace-LOCAL discover:-found
// candy (e.g. box/arch/candy/arch-pac-test) at all — it worked only by coincidence for a
// cross-repo @github-pinned ref (resolved via a from: path already absolute by the time it's
// discovered). uf.Namespaces[ns] is a *UnifiedFile carrying its OWN already-materialized .Candy map
// (populated by the ORIGINAL LoadUnified walk's per-namespace discover fold — the SAME data
// sub.AllBoxNames()/ResolveBox(sub,...) already prove reachable for BOXES), so
// uf.Namespaces[ns].ProjectCandies(dir) reads it directly — no second filesystem walk, no directory
// guessing, and it naturally covers BOTH inline candy: nodes and discover:-found ones (their From:
// path is already resolved absolute by the walk, so a stale/wrong dir passed here is harmless: the
// filepath.IsAbs(p) branch in projectCandiesScanned skips the join).
//
// Merged additively into the SAME rp.Candies/rp.CandyModels maps the root scan fills — a bare-ref
// key can never collide across namespaces for the SAME candy (same content, same key); a genuine
// name clash between two DIFFERENT candies sharing a bare name is a pre-existing
// resolver-arbitration concern (`charly box reconcile`), not something this fill introduces.
func fillNamespacedBoxes(uf *UnifiedFile, prefix, calver, dir string, opts ResolveOpts, rp *spec.ResolvedProject, visited map[*UnifiedFile]bool) {
	if uf == nil || visited[uf] {
		return
	}
	visited[uf] = true
	for ns, subUF := range uf.Namespaces {
		if subUF == nil {
			continue
		}
		child := ns
		if prefix != "" {
			child = prefix + "." + ns
		}
		sub := subUF.ProjectConfig()
		// subUF.projectCandiesScanned(subUF.RootDir) is the CORRECT local-candy source (reads
		// subUF.Candy directly — no re-load, no directory mismatch; see this function's doc
		// comment) fed into the SAME remote-fetch pipeline ScanAllCandyWithConfigOpts's root-scope
		// caller uses (scanCandyFromLocal — R3, one pipeline, not a duplicate), so a namespace's
		// candy set covers BOTH its own local discover:-found candies AND its cross-repo @github
		// pins. subUF.RootDir (not the outer dir) is REQUIRED here: a discovered candy's From:
		// path is relative to the NAMESPACE's own root dir (materializeDiscoveredNode), not the
		// caller's — falls back to the outer dir when RootDir is unset (a synthetic/test
		// UnifiedFile with no walk-assigned dir; matches this function's pre-fix behavior there).
		nsDir := subUF.RootDir
		if nsDir == "" {
			nsDir = dir
		}
		if localScanned, lErr := subUF.projectCandiesScanned(nsDir); lErr == nil {
			if nsLayers, err := scanCandyFromLocal(localScanned, sub, nsDir, opts); err == nil {
				for name, c := range nsLayers {
					if c == nil {
						continue
					}
					m, v, ok := rawCandyPair(c)
					if !ok {
						continue
					}
					if rp.Candies == nil {
						rp.Candies = map[string]spec.CandyView{}
						rp.CandyModels = map[string]spec.CandyModel{}
					}
					if _, exists := rp.CandyModels[name]; !exists {
						rp.Candies[name] = v
						rp.CandyModels[name] = m
					}
				}
			}
		}
		for _, name := range sub.AllBoxNames() {
			img, ok := sub.BoxConfig(name)
			if !ok || (!img.IsEnabled() && !opts.shouldIncludeDisabled(name)) {
				continue
			}
			resolved, err := ResolveBox(sub, name, calver, dir, opts)
			if err != nil {
				continue
			}
			view := projectResolvedBox(resolved)
			if rp.Boxes == nil {
				rp.Boxes = map[string]spec.ResolvedBoxView{}
			}
			rp.Boxes[child+"."+name] = view
		}
		fillNamespacedBoxes(subUF, child, calver, dir, opts, rp, visited)
	}
}

// projectTemplates decodes the uf.Local/K8s/Pod/VM/Android raw template maps (map[string]json.RawMessage)
// into the existing spec kind types — the resolved kind-template maps validate/check-include/status read.
// Returns nil when no template kind is present. K1-unblock wave 2: ALSO recurses into uf.Namespaces,
// mirroring fillBoxPlans's prefix-accumulation pattern exactly, so a namespace-qualified template ref
// (`local: <ns>.<tmpl>`, `kind:k8s` entity `<ns>.<name>`, …) is visible in the envelope too — previously
// only root-level names were. Purely ADDITIVE (qualified keys never collide with a bare name, since a
// bare name can never contain "."), so every existing root-scoped consumer is unaffected.
func projectTemplates(uf *UnifiedFile) *spec.ProjectTemplates {
	t := &spec.ProjectTemplates{}
	fillNamespacedTemplates(uf, "", t, map[*UnifiedFile]bool{})
	if t.Local == nil && t.K8s == nil && t.Pod == nil && t.VM == nil && t.Android == nil {
		return nil
	}
	return t
}

// fillNamespacedTemplates recursively copies uf's OWN template maps (qualified by prefix) into t, then
// descends into uf.Namespaces with the accumulated prefix. The visited set guards the pointer-keyed
// namespace cache against a self-referential cycle (mirrors fillBoxPlans's own guard).
func fillNamespacedTemplates(uf *UnifiedFile, prefix string, t *spec.ProjectTemplates, visited map[*UnifiedFile]bool) {
	if uf == nil || visited[uf] {
		return
	}
	visited[uf] = true
	// KIND-BLIND copy: the raw template bytes ride into the envelope verbatim as opaque RawBody. The
	// host NEVER decodes them into a concrete spec.<Kind> (that would be per-kind knowledge in the
	// kernel — a boundary-law violation the TestNoConcreteKindInKernel gate catches). The consuming
	// PLUGINS decode a RawBody into the concrete kind they need.
	cp := func(src map[string]json.RawMessage, dst *map[string]spec.RawBody) {
		for name, raw := range src {
			qualified := name
			if prefix != "" {
				qualified = prefix + "." + name
			}
			if *dst == nil {
				*dst = make(map[string]spec.RawBody, len(src))
			}
			(*dst)[qualified] = raw
		}
	}
	cp(uf.Local, &t.Local)
	cp(uf.K8s, &t.K8s)
	cp(uf.Pod, &t.Pod)
	cp(uf.VM, &t.VM)
	cp(uf.Android, &t.Android)
	for ns, sub := range uf.Namespaces {
		child := ns
		if prefix != "" {
			child = prefix + "." + ns
		}
		fillNamespacedTemplates(sub, child, t, visited)
	}
}

// buildResolvedProjectFromDir is the load+project entry the "resolved-project" host-builder wraps and
// host-side callers use directly. It loads the project (fail-fast — a load/resolve error aborts) via
// the shared loadProjectForResolve, then projects it. The error-TOLERANT sibling the validate-project
// seam uses is buildResolvedProjectTolerant (validate_project_host.go).
func buildResolvedProjectFromDir(dir string, opts ResolveOpts) (*spec.ResolvedProject, error) {
	lp, err := loadProjectForResolve(dir, opts, nil)
	if err != nil {
		return nil, err
	}
	if lp.empty {
		return &spec.ResolvedProject{}, nil
	}
	return projectResolvedProject(lp.cfg, lp.layers, lp.uf, lp.distroCfg, lp.builderCfg, lp.initCfg, dir, lp.version, opts, nil)
}

// hostBuildResolvedProject is the "resolved-project" host-builder (F11): resolve the project at
// req.Dir (empty = cwd) and return the generic spec.ResolvedProject envelope. Registered idempotently
// at package-var init, like every other hostBuilders entry.
func hostBuildResolvedProject(_ context.Context, req spec.ResolvedProjectRequest, _ buildEngineContext) (spec.ResolvedProject, error) {
	dir := req.Dir
	if dir == "" {
		d, err := os.Getwd()
		if err != nil {
			return spec.ResolvedProject{}, err
		}
		dir = d
	}
	if req.LocalSuperproject {
		restore := applySelfSuperprojectOverride(dir)
		defer restore()
	}
	rp, err := buildResolvedProjectFromDir(dir, ResolveOpts{IncludeDisabled: req.IncludeDisabled})
	if err != nil {
		return spec.ResolvedProject{}, err
	}
	return *rp, nil
}

var _ = func() bool {
	registerHostBuilder(resolvedProjectBuilderKind, typedHostBuilder(resolvedProjectBuilderKind, hostBuildResolvedProject))
	return true
}()
