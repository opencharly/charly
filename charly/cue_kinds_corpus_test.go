package main

// Generic corpus validation for every kind: for each node-form charly.yml,
// iterate its top-level `<name>: {<kind>: …}` nodes and validate each entity
// against #NodeDoc's per-entity grammar. Proves the registered schemas accept the
// whole real corpus.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"
)

// TestCueBox_Corpus validates every discovered box entity (node-form
// box/<distro>/box/<name>/charly.yml) against #Box.
func TestCueBox_Corpus(t *testing.T) {
	matches, err := filepath.Glob("../box/*/box/*/charly.yml")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var ok int
	for _, f := range matches {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		doc, err := cueDocFromYAML(f, data)
		if err != nil {
			t.Errorf("%s: ingest: %v", f, err)
			continue
		}
		// Unified node-form after EDGE-INHERIT cutover D: the `box:` kind merged
		// INTO `candy:`. A discovered box/<distro>/box/<name>/charly.yml entity is a
		// `<name>: {candy: {base|from: …}}` IMAGE (the former box:) — iterate the
		// top-level nodes and validate each node's `candy` discriminator value
		// against #Box (still the image def; `box`→#Box is registered as an internal
		// validation key). A candy carrying neither base: nor from: is a LAYER
		// fragment, not an image, so it is not validated here.
		it, ferr := doc.Fields()
		if ferr != nil {
			continue
		}
		for it.Next() {
			candy := it.Value().LookupPath(cue.ParsePath("candy"))
			if !candy.Exists() {
				continue
			}
			base := candy.LookupPath(cue.ParsePath("base"))
			from := candy.LookupPath(cue.ParsePath("from"))
			if !base.Exists() && !from.Exists() {
				continue // a layer fragment, not an image — validated as #Candy elsewhere
			}
			if verr := validateEntityCUE("box", f, candy); verr != nil {
				t.Errorf("FAIL %s", verr)
				continue
			}
			ok++
		}
	}
	t.Logf("box CUE validation: %d/%d discovered box entities validated", ok, len(matches))
	if ok == 0 {
		t.Fatal("no box entities validated (glob/path wrong?)")
	}
}

func nodeFormCorpusFiles() []string {
	return []string{
		"../charly.yml",          // repo root (pod/local/k8s/vm/check/android entities)
		"../box/arch/charly.yml", // box submodule stacks
		"../box/fedora/charly.yml",
		"../box/debian/charly.yml",
		"../box/ubuntu/charly.yml",
		"../box/cachyos/charly.yml",
		"charly.yml", // the binary-embedded default (distro/builder/init/resource/sidecar vocabulary), relative to charly/
	}
}

func TestCueKinds_Corpus(t *testing.T) {
	// Unified node-form discovery: a top-level `<name>: {<kind>: …}` node IS an
	// entity of <kind> (the legacy kind-keyed `<kind>: {<name>: …}` map is rejected
	// at load — node-form is the only authoring surface). Each
	// entity node is validated through #NodeDoc's per-entity pattern constraint
	// (`{[!~dir]: #Node}`) via FillPath — the SAME non-concrete, closedness-only
	// gate validateNodeDocCUE (the loader's validate-before-execute) uses, so the
	// per-kind #<Kind> def types each kind-value while the vm `source` disjunction
	// stays lazy (no spurious concrete "incomplete value" artifact).
	// C2-candy: every authoring kind is externalized — #Node is an OPEN struct with NO arms, so
	// KindWords is EMPTY and the #NodeDoc per-entity grammar is structural-only (validating a node
	// against it is now vacuous). The corpus VALUE gate moved to the KEPT per-kind value defs
	// (kindValueDef: candy → #CandyValue, pod/vm/k8s/local/android → #<Kind>Value) — the SAME
	// host-side gate the loader runs (validateKindValueCUE). So this corpus test validates each
	// node's inline discriminator value against its kept value def (non-concrete closedness),
	// proving the whole real corpus passes the host-side gate. Plugin kinds without a kept value
	// def (group/agent/module/…) are validated via their served plugin schema at runPluginKind and
	// skipped here (nodeHasPluginKindDisc).
	kinds := make([]string, 0, len(kindValueDef))
	for k := range kindValueDef {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	counts := map[string]int{}
	total := 0
	for _, f := range nodeFormCorpusFiles() {
		data, err := os.ReadFile(f)
		if err != nil {
			continue // layout may omit a file
		}
		// Register external deploy substrate words declared by this file's
		// discovered candies, so a deploy/bed using such a word (e.g.
		// check-exampledeploy -> exampledeploy) is recognized + skipped below — it
		// is validated via the loader/bed path, not the core #NodeDoc grammar this
		// test covers (the same exemption plugin KIND nodes get).
		prescanDeclaredPluginWords(data, filepath.Dir(f))
		doc, err := cueDocFromYAML(f, data)
		if err != nil {
			t.Errorf("%s: ingest: %v", f, err)
			continue
		}
		it, ierr := doc.Fields()
		if ierr != nil {
			t.Errorf("%s: fields: %v", f, ierr)
			continue
		}
		for it.Next() {
			name := it.Selector().Unquoted()
			if docDirectiveSet[name] {
				continue // version/repo/import/discover/defaults/provides — not entities
			}
			node := it.Value()
			// A node's kind = the single reserved ENTITY discriminator it carries.
			kind := ""
			for _, k := range kinds {
				if node.LookupPath(cue.ParsePath(k)).Exists() {
					kind = k
					break
				}
			}
			if kind == "" {
				// Not a CORE kind. It may be a PLUGIN kind (agent/module/package-group)
				// — validated by the plugin's served #<Kind>Input schema at
				// runPluginKind, NOT by #NodeDoc — so skip it here (this test covers the
				// core #NodeDoc grammar only; the plugin path is exercised by
				// LoadUnified + the per-plugin loader tests).
				if nodeHasPluginKindDisc(node) {
					continue
				}
				t.Errorf("FAIL %s:%s: no entity discriminator found in node-form node", f, name)
				continue
			}
			// Validate the node's inline discriminator value against its KEPT value def
			// (#CandyValue / #<Kind>Value) non-concrete — the host-side closedness gate
			// (validateKindValueCUE) over the real corpus.
			cv := node.LookupPath(cue.ParsePath(kind))
			vdef := sharedCueSchema.LookupPath(cue.ParsePath(kindValueDef[kind]))
			if vdef.Err() != nil {
				t.Errorf("FAIL %s: value def %s for kind %q not found: %v", f, kindValueDef[kind], kind, vdef.Err())
				continue
			}
			if verr := cv.Unify(vdef).Validate(); verr != nil {
				t.Errorf("FAIL %s:%s.%s: %s", f, kind, name, errors.Details(verr, nil))
				continue
			}
			counts[kind]++
			total++
		}
	}
	for _, kind := range kinds {
		t.Logf("kind %-9s: %d real entities validated", kind, counts[kind])
	}
	if total == 0 {
		t.Fatal("no real entities validated (node-form discovery wrong?)")
	}
}

// nodeHasPluginKindDisc reports whether a node's discriminator is a registered PLUGIN
// kind (a ClassKind provider that is NOT a core kindWordSet entry — agent / module /
// package-group). Such entities are validated by the plugin's served schema at
// runPluginKind, not by #NodeDoc, so the core-grammar corpus test skips them.
func nodeHasPluginKindDisc(node cue.Value) bool {
	it, err := node.Fields()
	if err != nil {
		return false
	}
	for it.Next() {
		k := it.Selector().Unquoted()
		if kindWordSet[k] {
			continue // a core kind — already handled by the kinds loop
		}
		if recognizedKind(k) {
			return true // a registered OR pre-scan-declared plugin kind (incl. external structural kinds) — validated via the loader/bed path, not core #NodeDoc here
		}
		if isExternalDeploySubstrate(k) {
			return true // an external (out-of-process) deploy substrate — validated via the loader/bed path, not core #NodeDoc here
		}
	}
	return false
}
