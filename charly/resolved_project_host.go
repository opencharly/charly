package main

import (
	"context"
	"os"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
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
// host-side directly (deploykit.ProjectResolvedBox / buildResolvedProjectFromDir). The
// per-concern config-resolve / status-substrate seams collapse into this one envelope at their
// consumers' later K5 units (the AI-harness check projection already did — plugin-check reads it here).

// resolvedProjectBuilderKind is the F11 hostBuilders key — a generic action noun, never a provider word.
const resolvedProjectBuilderKind = "resolved-project"

// projectResolvedProject is the host wrapper for the relocated envelope assembler
// (loaderkit.ProjectResolvedProject, K3 build-engine Unit 2). It builds the host-coupled seams (closures
// capturing opts + the registry) and delegates. When diags is nil it is FAIL-FAST (a per-box ResolveBox
// failure aborts with an error); when non-nil it is ERROR-TOLERANT (the validate-project path, which
// appends a diagnostic and skips the box).
func projectResolvedProject(cfg *Config, layers map[string]spec.CandyReader, uf *spec.UnifiedFile, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, initCfg *buildkit.InitConfig, dir, version string, opts loaderkit.ResolveOpts, diags *spec.Diagnostics) (*spec.ResolvedProject, error) {
	return projectResolvedProjectWithBoxes(cfg, layers, uf, distroCfg, builderCfg, initCfg, dir, version, opts, diags, nil)
}

// projectResolvedProjectWithBoxes is the host wrapper carrying the optional pre-resolved boxes map (the
// build-prep seam path preserves the render-prep caches; nil resolves fresh). It computes the wall-clock
// calver, applies the perf pre-fill (below), builds the seams, and calls the relocated assembler.
func projectResolvedProjectWithBoxes(cfg *Config, layers map[string]spec.CandyReader, uf *spec.UnifiedFile, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, initCfg *buildkit.InitConfig, dir, version string, opts loaderkit.ResolveOpts, diags *spec.Diagnostics, preResolvedBoxes map[string]*buildkit.ResolvedBox) (*spec.ResolvedProject, error) {
	// R1 fix (K1-unblock wave 2): pre-populate opts.DistroCfg/BuilderCfg from the ALREADY-LOADED values,
	// so the ResolveBox seam's fillBuildConfigFallback guard short-circuits instead of re-running a full
	// LoadUnified(dir) on EVERY ResolveBox call (the namespaced-box loop is the first real caller; live
	// timing showed 80 boxes × ~750ms reload = 69s otherwise). Behavior-identical (same dir → same
	// vocabulary every call). Stays HOST so the ResolveBox/FillNamespacedBoxes closures capture the
	// filled opts — keeping the relocated assembler opts-agnostic.
	if opts.DistroCfg == nil {
		opts.DistroCfg = distroCfg
	}
	if opts.BuilderCfg == nil {
		opts.BuilderCfg = builderCfg
	}
	calver := ComputeCalVer()
	seams := loaderkit.ResolveProjectSeams{
		ResolveBox: func(cfg *spec.Config, name, calver, dir string) (*buildkit.ResolvedBox, error) {
			bkopts, oerr := buildkitOptsWithVocab(dir, opts)
			if oerr != nil {
				return nil, oerr
			}
			return buildkit.ResolveBox(cfg, name, calver, dir, bkopts)
		},
		FillNamespacedBoxes: func(nsUF *spec.UnifiedFile, ic *buildkit.InitConfig, prefix, calver, dir string, rp *spec.ResolvedProject, visited map[*spec.UnifiedFile]bool) {
			fillNamespacedBoxes(nsUF, ic, prefix, calver, dir, opts, rp, visited)
		},
		ResolveResources:      resolveResources,
		ShouldIncludeDisabled: opts.ShouldIncludeDisabled,
		ComputeIntermediates:  ComputeIntermediates,
		ExternalizedBuilders:  externalizedBuilders,
	}
	rp, err := loaderkit.ProjectResolvedProject(cfg, layers, uf, distroCfg, builderCfg, initCfg, dir, version, calver, seams, diags, preResolvedBoxes)
	if rp != nil {
		// Primaries: the plugin-verb PRIMARY-field D-fact snapshot (loaderThreaded().Primaries),
		// carried on the resolved-project envelope for the deploy-tree resugar (envelope keystone, lane3).
		rp.Primaries = loaderThreaded().Primaries
	}
	return rp, err
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
// fillNamespacedBoxes recurses uf.Namespaces (a *spec.UnifiedFile tree — R1 fix, same wave: NOT
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
// Why *spec.UnifiedFile, not *Config (an R1 correction to this fix's OWN first draft): a first attempt
// called ScanAllCandyWithConfigOpts(dir, sub, opts) — but that function's LOCAL-candy discovery
// (scanLocalCandies) re-invokes LoadUnified(dir) FRESH, ignoring its cfg parameter entirely, so it
// just redundantly re-scanned the ROOT project and never found a namespace-LOCAL discover:-found
// candy (e.g. box/arch/candy/arch-pac-test) at all — it worked only by coincidence for a
// cross-repo @github-pinned ref (resolved via a from: path already absolute by the time it's
// discovered). uf.Namespaces[ns] is a *spec.UnifiedFile carrying its OWN already-materialized .Candy map
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
func fillNamespacedBoxes(uf *spec.UnifiedFile, initCfg *buildkit.InitConfig, prefix, calver, dir string, opts loaderkit.ResolveOpts, rp *spec.ResolvedProject, visited map[*spec.UnifiedFile]bool) {
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
		var nsLayers map[string]spec.CandyReader
		// projectCandiesScanned(subUF, subUF.RootDir) is the CORRECT local-candy source (reads
		// subUF.Candy directly — no re-load, no directory mismatch; see this function's doc
		// comment) fed into the SAME remote-fetch pipeline ScanAllCandyWithConfigOpts's root-scope
		// caller uses (scanCandyFromLocal — R3, one pipeline, not a duplicate), so a namespace's
		// candy set covers BOTH its own local discover:-found candies AND its cross-repo @github
		// pins. subUF.RootDir (not the outer dir) is REQUIRED here: a discovered candy's From:
		// path is relative to the NAMESPACE's own root dir (materializeDiscoveredNode), not the
		// caller's — falls back to the outer dir when RootDir is unset (a synthetic/test
		// spec.UnifiedFile with no walk-assigned dir; matches this function's pre-fix behavior there).
		nsDir := subUF.RootDir
		if nsDir == "" {
			nsDir = dir
		}
		if localScanned, lErr := projectCandiesScanned(subUF, nsDir); lErr == nil {
			if scanned, err := scanCandyFromLocal(localScanned, sub, opts); err == nil {
				nsLayers = scanned
				for name, c := range nsLayers {
					if c == nil {
						continue
					}
					m, v, ok := deploykit.RawCandyPair(c)
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
		// Resolve EVERY box in this namespace's own config first (so a sibling-base lookup —
		// parentCandies via deploykit.CandyProvidedByBox — resolves against an ALREADY-populated
		// map, mirroring NewGenerator+RenderPrepAll's two-phase shape for the root project), then
		// render-prep each one through the SAME deploykit.Generator.RenderPrepBox the root project
		// uses (R3 — no duplicated logic): a namespaced box that participates in ANOTHER project's
		// build (e.g. a builder/base referenced via `builder: <ns>.<name>` / `base: <ns>.<name>`, like
		// box/cachyos's `arch.arch`/`arch.arch-builder`) needs the SAME build-render caches
		// (BakedMetadata, RenderCandyOrder, …) a root box gets from RenderPrepAll — without them,
		// WriteLabels panics on a nil BakedMetadata the moment `charly box generate`/`build` reaches
		// it (RCA'd K1-alpha regression: this fill previously called bare ResolveBox only, which
		// never populates the render-render caches at all).
		bkopts, oerr := buildkitOptsWithVocab(dir, opts)
		if oerr != nil {
			continue // vocab load failed → skip this namespace (matches the former per-box ResolveBox erroring)
		}
		subBoxes := map[string]*buildkit.ResolvedBox{}
		for _, name := range sub.AllBoxNames() {
			img, ok := sub.BoxConfig(name)
			if !ok || (!img.IsEnabled() && !opts.ShouldIncludeDisabled(name)) {
				continue
			}
			resolved, err := buildkit.ResolveBox(sub, name, calver, dir, bkopts)
			if err != nil {
				continue
			}
			subBoxes[name] = resolved
		}
		if len(subBoxes) > 0 {
			tempGen := &Generator{Config: sub, Candies: nsLayers, InitConfig: initCfg, Dir: dir, Boxes: subBoxes}
			for name, resolved := range subBoxes {
				fullKey := child + "." + name
				// A box the CURRENT build actually needs (e.g. box/cachyos's `cachyos-pacstrap-
				// builder` basing on `arch.arch`) is ALREADY correctly present in rp.Boxes by this
				// point — the build-prep seam's own buildkit.ResolveAllBox->resolveNamespacedBases
				// pull (demand-driven, requalifying Base/Builder to the fully-qualified ancestor,
				// e.g. arch-builder's `base: arch` -> `arch.arch`) plus this function caller's own
				// auto-intermediates fold already added it, render-prepped, with correctly
				// requalified cross-references. THIS loop's bare `ResolveBox(sub, name, …)` does
				// NOT requalify Base/Builder (they stay namespace-relative, e.g. plain "arch") — a
				// harmless orientation-only gap for boxes nobody's build actually uses (never fed
				// to Generate(order), so a stale Base never gets dereferenced), but overwriting an
				// ALREADY-correct entry with this uncorrected one breaks the real build the moment
				// Generate resolves that box's base image (RCA'd K1-alpha regression #2: fixing
				// WriteLabels's nil BakedMetadata via render-prep here, uncovered THIS pre-existing
				// gap one layer deeper — ResolveBaseImage panics on the non-requalified `arch`
				// lookup finding no such key in dg.Boxes). Never overwrite a demand-pulled entry.
				if _, exists := rp.Boxes[fullKey]; exists {
					continue
				}
				// Best-effort, matching this fill's existing tolerance: a namespaced box whose
				// render-prep fails (e.g. a required capability missing) is projected WITHOUT the
				// render caches rather than dropped outright — it just can't be USED as a builder/
				// base stage, exactly as before this fix for every OTHER box in this loop.
				_ = tempGen.toDeploykit().RenderPrepBox(name)
				view := deploykit.ProjectResolvedBox(resolved)
				if rp.Boxes == nil {
					rp.Boxes = map[string]spec.ResolvedBoxView{}
				}
				rp.Boxes[fullKey] = view
			}
		}
		fillNamespacedBoxes(subUF, initCfg, child, calver, dir, opts, rp, visited)
	}
}

// buildResolvedProjectFromDir is the load+project entry the "resolved-project" host-builder wraps and
// host-side callers use directly. It loads the project (fail-fast — a load/resolve error aborts) via
// the shared loadProjectForResolve, then projects it. The error-TOLERANT sibling the validate-project
// seam uses is buildResolvedProjectTolerant (validate_project_host.go).
func buildResolvedProjectFromDir(dir string, opts loaderkit.ResolveOpts) (*spec.ResolvedProject, error) {
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
	rp, err := buildResolvedProjectFromDir(dir, loaderkit.ResolveOpts{IncludeDisabled: req.IncludeDisabled, ExtraCandyRefs: req.ExtraCandyRefs})
	if err != nil {
		return spec.ResolvedProject{}, err
	}
	return *rp, nil
}

var _ = func() bool {
	registerHostBuilder(resolvedProjectBuilderKind, typedHostBuilder(resolvedProjectBuilderKind, hostBuildResolvedProject))
	return true
}()
