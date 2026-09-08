package main

import (
	"fmt"
	"path/filepath"

	"github.com/opencharly/spec/merge"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// materialize.go — the host-coupled leaf LEGS of project materialize (#46/K1/#48). The per-document/
// per-namespace root-wins MERGE ORCHESTRATION (the former materializeLoadedProject) RELOCATED to
// sdk/loaderkit (loaderkit.MaterializeLoadedProject, task #48) — a kind-blind walk+merge MECHANISM
// that never touches the registry, so it belongs in the sdk kit consumed by plugins, exactly like
// LoadUnified (#47). This file keeps ONLY the genuinely host-/registry-/bootstrap-coupled leaf legs
// the loaderkit orchestration calls back through (hostMaterializeProjectSeams): the per-document
// registry kind-decode (materializeProject → the registered spec.Materializer), the bootstrap-candy-
// routed discovered-manifest fold (foldDiscoveredManifests / materializeDiscoveredNode — the
// parsed-node box⊻layer routing (CandyIsImage, parser consolidation F2.1) shared with
// ApplyDiscover, R3), and the
// binary-embedded default vocabulary (applyEmbeddedDefaults / materializeDocStream).
//
// CLASSIFICATION (K1 unit 1 / #48). The ACTUAL registry resolve + live Provider dispatch stays core
// (clause M, unchanged — provider_kind_invoke.go + provider_registry.go). The per-node NOT-FOUND
// policy is candy/plugin-loader (loaderkit.Materialize). The walk/merge ORCHESTRATION is
// sdk/loaderkit (loaderkit.MaterializeLoadedProject). What remains here are the three host leaf legs
// that ARE registry-/bootstrap-/host-coupled — reached kind-blind by the loaderkit orchestration.

// hostMaterializeProjectSeams wires charly's three host-coupled materialize leaf legs into the
// spec.MaterializeProjectSeams the relocated orchestration (loaderkit.MaterializeLoadedProject,
// #48) calls back through. The compiled-in placement reaches each leg DIRECTLY (zero marshal); the
// out-of-module plugin path (candy/plugin-fleet's execLoaderExecutor) drives the SAME orchestration
// over the single "loader-materialize" host leg (host_build_loader_floor.go), which constructs these SAME
// seams — so both placements are byte-identical.
func hostMaterializeProjectSeams() spec.MaterializeProjectSeams {
	return spec.MaterializeProjectSeams{
		MaterializeProject:      materializeProject,
		FoldDiscoveredManifests: foldDiscoveredManifests,
		ApplyEmbeddedDefaults:   applyEmbeddedDefaults,
	}
}

// foldDiscoveredManifests folds every discovered manifest's parsed nodes into uf
// — the SHARED loop (R3) both loaderkit.MaterializeLoadedProject's step 2 (the LoadUnified
// walk path, via the FoldDiscoveredManifests seam) AND ApplyDiscover (unified.go, the layers candy-scan path) drive over
// their respective []spec.DiscoveredManifest.
func foldDiscoveredManifests(dms []spec.DiscoveredManifest, uf *spec.UnifiedFile) error {
	for i := range dms {
		dm := &dms[i]
		for j := range dm.Docs {
			pp := &dm.Docs[j]
			for k := range pp.Nodes {
				if err := materializeDiscoveredNode(pp.Nodes[k], dm.Dir, dm.RootDir, dm.Manifest, uf); err != nil {
					return fmt.Errorf("%s: %w", dm.Dir, err)
				}
			}
		}
	}
	return nil
}

// materializeDiscoveredNode folds ONE discovered manifest node into uf — the SINGLE per-node
// handler foldDiscoveredManifests drives. A LAYER candy registers a lazy `From:` directory
// reference (scanCandy parses it later; explicit entry wins); every other kind materializes via the
// registered spec.Materializer (materializeNodeInto, K1 unit 1). The box⊻layer pre-check is the
// PARSED-NODE routing (sdk/loaderkit CandyIsImage, reached through the loader seam — parser
// consolidation F2.1; the former genericNode reconstruction is deleted).
func materializeDiscoveredNode(pn spec.ParsedNode, dir, rootDir, manifest string, uf *spec.UnifiedFile) error {
	// F3.1: ask the PROVIDER via the word-keyed capability carrier — never compare pn.Disc to
	// the "candy" literal. Resolve the disc's provider through the registry (word-keyed DATA,
	// the same resolve decodeEntityViaRegistry performs), then ask the carrier: the compiled-in
	// candy/plugin-candy-kind answers IsCandyKind() true; a non-candy or unknown disc falls
	// through to materializeNodeInto (its not-found policy).
	if prov, ok := providerRegistry.ResolveKind(pn.Disc); ok {
		if ck, isCandy := prov.(spec.CandyKindCarrier); isCandy && ck.IsCandyKind() {
			if !requireProjectLoader().CandyIsImage(pn) {
				name := filepath.Base(dir)
				if _, exists := uf.Candy[name]; exists {
					return nil // explicit entry wins
				}
				rel, relErr := filepath.Rel(rootDir, dir)
				if relErr != nil {
					rel = dir
				}
				uf.SetCandy(name, &spec.InlineCandy{From: rel, Manifest: manifest})
				return nil
			}
		}
	}
	return materializeNodeInto(pn, uf)
}

// materializeDocStream parses an in-memory node-form YAML document STREAM (the binary-embedded
// default vocabulary — no imports, no discover, no namespaces) and materializes every document into
// uf. The per-document pipeline is the ONE loaderkit doc-stream composer
// (spec.ProjectLoader.ParseDocStream → loaderkit.ParseDocStream, parser consolidation F2.5) — the
// SAME classify → #NodeDoc gate → registered parser → directive serialization loop the file walk
// drives, minus the file walk; the host replays the two host-coupled leaf legs over the returned
// docs: per-document registry kind-decode (materializeProject) + root-wins merge. The embedded
// vocab has no reserved directives (import/discover) to consume. srcLabel labels diagnostics.
func materializeDocStream(data []byte, srcLabel string, uf *spec.UnifiedFile) error {
	docs, imports, scanSpecs, err := requireProjectLoader().ParseDocStream(data, srcLabel, "", hostWalkSeams())
	if err != nil {
		return err
	}
	// An embedded stream cannot declare imports/discover (the embedded vocabulary is a fixed
	// build-time asset) — reject the impossible loudly instead of silently ignoring it.
	if len(imports) > 0 || len(scanSpecs) > 0 {
		return fmt.Errorf("%s: embedded defaults declare imports/discover — not allowed in the binary vocabulary", srcLabel)
	}
	for i := range docs {
		label := docs[i].SrcLabel
		var sub spec.UnifiedFile
		if len(docs[i].Directives) > 0 {
			// Decode the RAW reserved-directive mapping (YAML) into a sub spec.UnifiedFile — the
			// SAME decode the walk envelope's MaterializeLoadedProject performs (honoring the
			// custom YAML unmarshalers on import/discover).
			if derr := yaml.Unmarshal(docs[i].Directives, &sub); derr != nil {
				return fmt.Errorf("%s: decoding directives: %w", label, derr)
			}
		}
		if err := materializeProject(&docs[i].Project, &sub); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		sub.Import = nil
		merge.MergeUnified(uf, &sub, "")
	}
	return nil
}

// materializeNodeInto folds ONE parsed node into uf via the registered spec.Materializer plugin
// (K1 unit 1, #46) — the not-found DISPATCH POLICY lives in candy/plugin-loader
// (loaderkit.Materialize), reached through requireMaterializer(); the actual registry resolve +
// provider dispatch stays host-side behind the DecodeEntity/BuildDeployEntity seam callbacks
// (hostMaterializeSeams, loader_threaded.go) — clause M never leaves charly core.
//
// Pre-seeds the spec.MaterializedProject accumulator from uf's CURRENT maps (so repeated calls
// across a document's node list ACCUMULATE rather than reset — maps are reference types, so this is
// a cheap map-header copy, not a deep copy) and copies the result back after.
//
// (K-wave 2 cone R1 unit C): this file owns every other
// host-coupled materialize leg AND hostMaterializeProjectSeams, the seam these two are reached
// through, so the accumulator plumbing belongs beside them rather than beside the deleted
// genericNode bridge it used to share a file with (parser consolidation F2.1).
func materializeNodeInto(pn spec.ParsedNode, uf *spec.UnifiedFile) error {
	acc := spec.MaterializedProject{
		Box: uf.Box, Candy: uf.Candy, Deploy: uf.Deploy, PluginKinds: uf.PluginKinds,
	}
	if err := requireMaterializer().MaterializeNode(pn, loaderThreaded(), hostMaterializeSeams(), &acc); err != nil {
		return err
	}
	uf.Box, uf.Candy, uf.Deploy, uf.PluginKinds = acc.Box, acc.Candy, acc.Deploy, acc.PluginKinds
	return nil
}

// materializeProject folds a whole spec.ParsedProject (the loader plugin's WalkProject reply — one
// document's decomposed nodes) into the typed spec.UnifiedFile, node by node. This is the exact
// host-side entry the plugin dispatch drops into: the registered loader plugin's spec.ProjectWalker
// (candy/plugin-loader, over sdk/loaderkit.Walk) builds the spec.LoadedProject/ParsedProject and
// loaderkit.MaterializeLoadedProject calls this per document via the MaterializeProject host seam
// (#48) this file wires.
func materializeProject(pp *spec.ParsedProject, uf *spec.UnifiedFile) error {
	if pp == nil {
		return nil
	}
	for i := range pp.Nodes {
		if err := materializeNodeInto(pp.Nodes[i], uf); err != nil {
			return err
		}
	}
	return nil
}
