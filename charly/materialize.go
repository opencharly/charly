package main

import (
	"bytes"
	"fmt"
	"path/filepath"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// materialize.go — the registry-coupled MATERIALIZE + root-wins MERGE half of project loading
// (#46). K1 split LoadUnified into two halves at a kind-blind seam: the kind-blind WALK+PARSE
// (import queue + discover + namespaced-import mounts + per-document parse) is reached via the
// registered loader plugin's spec.ProjectWalker (hostWalkProject, loader_threaded.go) and returns a
// generic spec.LoadedProject; THIS file replays the host's decode→materialize→merge over that
// envelope, reconstructing the typed *UnifiedFile exactly as the former inline loadUnifiedInto did.
//
// WHY THIS STAYS CORE (the kernel/plugin boundary law, CLAUDE.md "The kernel/plugin boundary law"):
// the MATERIALIZE is the kind-decode DISPATCH — clause (M), one of the four kind-blind Mechanisms
// the boundary law permits to live in `charly/` (plugin loading, prescan-dispatch, the kind-decode
// MATERIALIZE, and the wire broker; every OTHER kind-blind mechanism — parse/render/resolve/walk/
// engine — belongs to an sdk kit consumed by plugins). It dispatches PURELY by WORD against the
// provider registry (`materializeProject` → `normalizeNodeInto` → the reserved-word table in
// `reserved_registry.go`), never branching on a concrete kind in a compiled-in switch, so it never
// becomes an "incomplete seam" the boundary law would flag. This is why K1 deliberately split the
// WALK (a plugin-owned Mechanism, sdk/loaderkit, reached via ProjectWalker) from the MATERIALIZE
// (a core-owned Mechanism, this file) at exactly this seam, rather than moving both out together.

// materializeLoadedProject replays the host's MATERIALIZE + root-wins MERGE over a walk envelope,
// reconstructing the typed *UnifiedFile identically to the former inline loadUnifiedInto:
//  1. each document (root file + flat imports, in walk order) — decode its reserved directives into
//     a fresh sub UnifiedFile, materialize its parsed nodes (registry kind-decode), then root-wins
//     merge the sub into merged (first-seen wins → root wins);
//  2. the discovered manifests — register a lazy layer-candy `From:` reference OR materialize the
//     node, explicit-entry-wins (the SAME per-node handler applyDiscoveredManifest uses, R3);
//  3. the binary-embedded default vocabulary (project-wins);
//  4. the mounted namespace subtrees — recurse into merged.Namespaces[alias].
func materializeLoadedProject(lp *spec.LoadedProject, merged *UnifiedFile, byID map[int64]*UnifiedFile) error {
	// Register THIS project's *UnifiedFile under its walk-assigned id BEFORE recursing into its
	// namespaces, so a namespaced cycle-back / diamond REFERENCE mount nested in this subtree
	// resolves to this SAME pointer — the pointer identity the former loadNamespaceCached preserved
	// (the intentional main↔cachyos mutual import). byID persists across the WHOLE materialize.
	if lp.ID != 0 {
		byID[lp.ID] = merged
	}
	// 1. Documents (root + flat imports) — root-wins merge, in walk order.
	for i := range lp.Docs {
		d := &lp.Docs[i]
		var sub UnifiedFile
		if len(d.Directives) > 0 {
			// Decode the RAW reserved-directive mapping (YAML) into a sub UnifiedFile — the EXACT
			// decode the former mergeUnifiedDocs did (dirMap → Decode(&sub)), honoring the custom
			// YAML unmarshalers on import/discover.
			if err := yaml.Unmarshal(d.Directives, &sub); err != nil {
				return fmt.Errorf("%s: decoding directives: %w", d.SrcLabel, err)
			}
		}
		// Materialize the document's parsed entity nodes into sub (registry kind-decode).
		if err := materializeProject(&d.Project, &sub); err != nil {
			return fmt.Errorf("%s: %w", d.SrcLabel, err)
		}
		// Imports are already resolved + flattened into lp.Docs by the walk — drop the sub's Import
		// so the merge never re-processes them (the former mergeUnifiedDocs cleared sub.Import too).
		sub.Import = nil
		normalizeV4Aliases(&sub)
		mergeUnified(merged, &sub, d.SrcDir)
	}
	// 2. Discovered manifests (explicit-entry-wins), applied after the documents.
	for i := range lp.Discovered {
		dm := &lp.Discovered[i]
		for j := range dm.Docs {
			pp := &dm.Docs[j]
			for k := range pp.Nodes {
				gn, err := parsedNodeToGeneric(pp.Nodes[k])
				if err != nil {
					return fmt.Errorf("%s: %w", dm.Dir, err)
				}
				if err := materializeDiscoveredNode(gn, dm.Dir, dm.RootDir, dm.Manifest, merged); err != nil {
					return fmt.Errorf("%s: %w", dm.Dir, err)
				}
			}
		}
	}
	// 3. Binary-embedded default vocabulary (project-wins).
	if err := applyEmbeddedDefaults(merged); err != nil {
		return err
	}
	// 4. Mounted namespaces — each an isolated child UnifiedFile. A REFERENCE mount (cycle-break /
	// diamond) resolves to the SAME *UnifiedFile already registered under its target id (pointer
	// identity preserved); a DEFINITION mount materializes its inline child fresh.
	for i := range lp.Namespaces {
		nm := lp.Namespaces[i]
		if nm == nil {
			continue
		}
		if merged.Namespaces == nil {
			merged.Namespaces = map[string]*UnifiedFile{}
		}
		if nm.Ref {
			shared := byID[nm.RefID]
			if shared == nil {
				return fmt.Errorf("namespace %q: dangling reference to project id %d", nm.Alias, nm.RefID)
			}
			merged.Namespaces[nm.Alias] = shared
			continue
		}
		sub := &UnifiedFile{}
		if err := materializeLoadedProject(&nm.Project, sub, byID); err != nil {
			return err
		}
		merged.Namespaces[nm.Alias] = sub
	}
	return nil
}

// materializeDiscoveredNode folds ONE discovered manifest node into uf — the SINGLE per-node
// handler shared by materializeLoadedProject (the LoadUnified walk path) AND applyDiscoveredManifest
// (the layers candy-scan path), R3. A LAYER candy registers a lazy `From:` directory reference
// (scanCandy parses it later; explicit entry wins); every other kind materializes inline via
// normalizeNodeInto. The candyIsImage pre-check stays core (bootstrap-critical box⊻layer routing).
func materializeDiscoveredNode(gn *genericNode, dir, rootDir, manifest string, uf *UnifiedFile) error {
	if gn.disc == "candy" && !candyIsImage(gn) {
		name := filepath.Base(dir)
		if _, exists := uf.Candy[name]; exists {
			return nil // explicit entry wins
		}
		rel, relErr := filepath.Rel(rootDir, dir)
		if relErr != nil {
			rel = dir
		}
		uf.SetCandy(name, &InlineCandy{From: rel, Manifest: manifest})
		return nil
	}
	return normalizeNodeInto(gn, uf)
}

// materializeDocStream parses an in-memory node-form YAML document STREAM (the binary-embedded
// default vocabulary — no imports, no discover, no namespaces) and materializes every document into
// uf: the SAME classify → #NodeDoc gate → parse → decode-directives → materialize → merge the walk
// path runs per document, minus the file walk. Replaces the former embeddedDefaults()
// mergeUnifiedDocs call (K1 deleted mergeUnifiedDocs). The embedded vocab has no reserved
// directives (import/discover) to consume, so this stays a plain host parse — it does not touch the
// walk. srcLabel labels diagnostics.
func materializeDocStream(data []byte, srcLabel string, uf *UnifiedFile) error {
	parser := requireLoaderParser()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for docIdx := 0; ; docIdx++ {
		var node yaml.Node
		if err := decoder.Decode(&node); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("%s:doc%d: %w", srcLabel, docIdx, err)
		}
		shape, err := kit.ClassifyDoc(&node)
		if err != nil {
			return fmt.Errorf("%s:doc%d: %w", srcLabel, docIdx, err)
		}
		if shape != kit.DocShapeNode {
			continue
		}
		label := fmt.Sprintf("%s:doc%d", srcLabel, docIdx)
		raw, err := yaml.Marshal(&node)
		if err != nil {
			return fmt.Errorf("%s: re-marshal node-form doc: %w", label, err)
		}
		if err := validateNodeDocCUE(label, raw); err != nil {
			return err
		}
		directives, pp, err := parser.ParseDoc(&node, loaderThreaded())
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		var sub UnifiedFile
		if len(directives) > 0 {
			dirMap := &yaml.Node{Kind: yaml.MappingNode}
			for k, v := range directives {
				dirMap.Content = append(dirMap.Content, kit.ScalarNode(k), v)
			}
			if derr := dirMap.Decode(&sub); derr != nil {
				return fmt.Errorf("%s: decoding directives: %w", label, derr)
			}
		}
		if err := materializeProject(&pp, &sub); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		sub.Import = nil
		normalizeV4Aliases(&sub)
		mergeUnified(uf, &sub, "")
	}
	return nil
}
