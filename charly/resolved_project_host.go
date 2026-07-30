package main

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// resolved_project_host.go — the resolved-project envelope's namespaced-box FILL (K5-Unit-0, the
// S-K5 keystone's last host-resident remainder). #55 step3 unit 3b relocated the "resolved-project"
// HostBuild seam itself to candy/plugin-build's `build:project` word (resolveProjectEnvelope in
// candy/plugin-build/resolve_project_word.go), reached by its ~8 former HostBuild consumers via
// InvokeProvider peer-dispatch instead; unit 3-I relocated the "validate-project" seam's error-
// TOLERANT half onto the SAME word's OpValidate leg (candy/plugin-build/resolve_project_tolerant.go);
// unit 3-II (task #71) relocated the pod-overlay seam's own envelope fetch (build_overlay.go's
// hostBuildOverlay now fetches its render-prepped envelope plugin-side via
// InvokeProvider("build","generate",sdk.OpResolve,…) instead of calling anything in this file) AND
// deleted the now-fully-production-dead projectResolvedProjectWithBoxes WRAPPER this file used to
// carry (zero production callers once the overlay seam stopped calling it — its two former test
// callers were repointed: resolved_project_namespace_test.go's namespaced-box test now drives the
// LIVE hostBuildNamespaced directly; plugin_installstep_envelope_parity_test.go's generic-envelope
// use now calls a test-local reproduction, testProjectResolvedProjectWithBoxes in
// resolved_project_host_test.go — the wrapper's logic, kept test-side since no OTHER core-resident
// caller needs that exact "project an envelope from a live cfg/layers/uf/pre-resolved-boxes" shape
// anymore, R3).
//
// What REMAINS in this file is fillNamespacedBoxes alone — a genuinely still-needed capability with
// no other owner: host_build_buildengine.go's hostBuildNamespaced calls it DIRECTLY (not through
// any wrapper) on behalf of candy/plugin-build's own build/generate drive's namespaced-box
// resolution, a live production concern unrelated to the overlay envelope 3-II relocated. A future
// cutover that relocates hostBuildNamespaced's own caller onto a plugin-side namespaced-box
// resolution is the actual named exit for this file's full deletion — NOT tracked here as a new
// task; flagged to the #55 step3 orchestrator for the next fabric-tail wave.
//
// fillNamespacedBoxes is a DATA PROJECTION over the resolve engines that already exist — ResolveBox
// (per enabled box) + ScanAllCandy + the folded uf.Bundle deploy tree — serialized into the generic
// spec.ResolvedProject. It is NOT a new engine: it copies fields the existing engines populate,
// dropping the host-only json:"-" compute-cache pointers of ResolvedBox that are never wire data
// (DistroConfig/DistroDef/BuilderConfig/InitSystem/InitDef/CandyCaps).

// fillNamespacedBoxes populates *out with a namespace-QUALIFIED spec.ResolvedBoxView (`fedora.jupyter`,
// or `ns1.ns2.name` for a nested import) for every box reachable from cfg's import namespaces,
// recursively (the caller fills cfg's OWN root-scope boxes separately — hostBuildNamespaced's own
// scratch envelope carries ONLY the namespaced additions this function contributes; the general
// root+namespaced projection lives in testProjectResolvedProjectWithBoxes's production-equivalent
// recipe, resolved_project_host_test.go). A namespaced box that fails to resolve (e.g. references a
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
func fillNamespacedBoxes(uf *spec.UnifiedFile, initCfg *spec.InitConfig, prefix, calver, dir string, opts spec.ResolveOpts, rp *spec.ResolvedProject, visited map[*spec.UnifiedFile]bool) {
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
		// map, mirroring the deleted NewGenerator+RenderPrepAll's two-phase shape the root project's
		// resolve still follows (now via candy/plugin-build's resolveBuildEngine, #55 step3 3-II)), then
		// render-prep each one through the SAME deploykit.Generator.RenderPrepBox the root project
		// uses (R3 — no duplicated logic): a namespaced box that participates in ANOTHER project's
		// build (e.g. a builder/base referenced via `builder: <ns>.<name>` / `base: <ns>.<name>`, like
		// box/cachyos's `arch.arch`/`arch.arch-builder`) needs the SAME build-render caches
		// (BakedMetadata, RenderCandyOrder, …) a root box gets from RenderPrepAll — without them,
		// WriteLabels panics on a nil BakedMetadata the moment `charly box generate`/`build` reaches
		// it (RCA'd K1-alpha regression: this fill previously called bare ResolveBox only, which
		// never populates the render-render caches at all).
		vopts, oerr := resolveVocabOpts(dir, opts)
		if oerr != nil {
			continue // vocab load failed → skip this namespace (matches the former per-box ResolveBox erroring)
		}
		// #55 Cluster-B: the buildkit-coupled render-prep DRIVE (per-box ResolveBox +
		// RenderPrepBox + ProjectResolvedBox) moved to the deploykit box-resolve bridge, because
		// it WRITES buildkit host-render caches (BakedMetadata/RenderCandyOrder/…) no *spec.ResolvedBox
		// holds — a namespaced box that participates in ANOTHER project's build (a builder/base via
		// `builder: <ns>.<name>` / `base: <ns>.<name>`, like box/cachyos's `arch.arch`) needs those
		// caches or WriteLabels panics on nil BakedMetadata. FillNamespaceBoxViews SKIPS a box the
		// build already demand-pulled into rp.Boxes with correctly-requalified Base/Builder (never
		// overwrites it with this loop's non-requalified bare resolve — RCA'd K1-alpha regression
		// #2). Byte-equivalent to the former in-core inner block; the render-prep + skip semantics
		// are preserved verbatim inside the helper.
		deploykit.FillNamespaceBoxViews(sub, nsLayers, initCfg, child, calver, dir, specResolveOpts(vopts), rp)
		fillNamespacedBoxes(subUF, initCfg, child, calver, dir, opts, rp, visited)
	}
}

// The former host-side resolved-project projector function, its host-builder registration, and
// its F10 kind-const are ALL DELETED (#55 step3 unit 3b — see the repo CHANGELOG for their former
// names) — that capability moved to candy/plugin-build's `build:project` word (resolveProjectEnvelope), reached
// via InvokeProvider by its former ~8 consumers. buildResolvedProjectTolerant + this file's own
// projectResolvedProject wrapper are ALSO DELETED (#55 step3 unit 3-I) — the tolerant envelope
// projection moved to the SAME build:project word's OpValidate leg
// (candy/plugin-build/resolve_project_tolerant.go); loadProjectForResolve (validate_project_host.go)
// stays, now feeding ONLY the host-natural raw-config checks (hostBuildValidateProjectChecks) rather
// than an envelope projection. #55 step3 unit 3-II relocated the overlay seam's own envelope fetch
// (build_overlay.go no longer calls loadProjectForResolve at all) AND DELETED
// projectResolvedProjectWithBoxes itself (production-dead once the overlay seam stopped calling it
// — radical-dead-code-removal, per validator finding on PR #196; its two former test callers were
// repointed, see this file's header). fillNamespacedBoxes STAYS: host_build_buildengine.go's
// hostBuildNamespaced calls it directly for plugin-build's own namespaced-box resolution, a
// still-live, unrelated production concern — see this file's header for the full-deletion story.
