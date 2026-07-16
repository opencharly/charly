package main

// Generic corpus validation for every kind: for each node-form charly.yml,
// iterate its top-level `<name>: {<kind>: …}` nodes and validate each entity
// against #NodeDoc's per-entity grammar. Proves the registered schemas accept the
// whole real corpus.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// parseCorpusDocs decodes a corpus file's YAML multi-document stream and runs
// each document through the SAME parse the loader uses (activeLoaderParser.ParseDoc) — which desugars
// every plan step's `<word>: <input>` plugin sugar IN PLACE, so the CUE value
// gates below see the internal plugin/plugin_input form exactly as the loader's
// validate-before-execute gate does.
func parseCorpusDocs(t *testing.T, f string, data []byte) []*genericNode {
	t.Helper()
	var all []*genericNode
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Errorf("%s: yaml: %v", f, err)
			break
		}
		nodes, err := genericNodesFromDoc(&doc)
		if err != nil {
			t.Errorf("FAIL %s: parse: %v", f, err)
			continue
		}
		all = append(all, nodes...)
	}
	return all
}

// boxSubmoduleCheckedOut reports whether the box/<distro> submodule rooted at
// boxDir is checked out in THIS worktree. An un-inited git submodule leaves an
// EMPTY gitlink placeholder directory, so os.Stat(boxDir) is a false-positive
// "present"; the submodule's top-level charly.yml manifest is the reliable
// "checked out" signal. The box-corpus tests use it to skip GRACEFULLY (rather
// than hard-fail) on a box-less checkout, so `go test ./...` is green when box/*
// is not inited while still validating the corpus when it is.
func boxSubmoduleCheckedOut(boxDir string) bool {
	_, err := os.Stat(filepath.Join(boxDir, "charly.yml"))
	return err == nil
}

// TestCueBox_Corpus validates every discovered box entity (node-form
// box/<distro>/box/<name>/charly.yml) against #Box.
func TestCueBox_Corpus(t *testing.T) {
	matches, err := filepath.Glob("../box/*/box/*/charly.yml")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		// Distinguish "box submodules not checked out" (genuinely absent → skip so
		// the suite stays green on a box-less checkout) from "box present but the
		// discovery glob is wrong" (a real regression → fall through to the ok==0
		// t.Fatal below). A checked-out box submodule always has its top-level
		// box/<distro>/charly.yml manifest; an un-inited one leaves only an empty
		// gitlink placeholder dir.
		distroDirs, _ := filepath.Glob("../box/*")
		anyCheckedOut := false
		for _, d := range distroDirs {
			if boxSubmoduleCheckedOut(d) {
				anyCheckedOut = true
				break
			}
		}
		if !anyCheckedOut {
			t.Skip("no box/* submodule checked out — no box corpus to validate")
		}
	}
	var ok int
	for _, f := range matches {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		// Unified node-form after EDGE-INHERIT cutover D: the `box:` kind merged
		// INTO `candy:`. A discovered box/<distro>/box/<name>/charly.yml entity is a
		// `<name>: {candy: {base|from: …}}` IMAGE (the former box:) — parse (which
		// desugars the inline plan) and validate each node's `candy` discriminator
		// value against #Box (still the image def; `box`→#Box is registered as an
		// internal validation key). A candy carrying neither base: nor from: is a
		// LAYER fragment, not an image, so it is not validated here.
		for _, gn := range parseCorpusDocs(t, f, data) {
			if gn.disc != "candy" || gn.discValue == nil || gn.discValue.Kind != yaml.MappingNode {
				continue
			}
			if kit.MapValue(gn.discValue, "base") == nil && kit.MapValue(gn.discValue, "from") == nil {
				continue // a layer fragment, not an image — validated as #Candy elsewhere
			}
			b, merr := yaml.Marshal(gn.discValue)
			if merr != nil {
				t.Errorf("%s: marshal %q: %v", f, gn.name, merr)
				continue
			}
			candy, cerr := cueDocFromYAML(f, b)
			if cerr != nil {
				t.Errorf("%s: ingest %q: %v", f, gn.name, cerr)
				continue
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
	// (spec.KindValueDefs: candy → #CandyValue, pod/vm/k8s/local/android → #<Kind>Value) — the SAME
	// host-side gate the loader runs (validateKindValueCUE). So this corpus test validates each
	// node's inline discriminator value against its kept value def (non-concrete closedness),
	// proving the whole real corpus passes the host-side gate. Plugin kinds without a kept value
	// def (group/agent/module/…) are validated via their served plugin schema at runPluginKind and
	// skipped here (nodeHasPluginKindDisc).
	kinds := make([]string, 0, len(spec.KindValueDefs))
	for k := range spec.KindValueDefs {
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
		// check-exampledeploy -> exampledeploy) parses as an entity below — it
		// is validated via the loader/bed path, not the kept core value defs this
		// test covers (the same exemption plugin KIND nodes get).
		prescanDeclaredPluginWords(data, filepath.Dir(f))
		for _, gn := range parseCorpusDocs(t, f, data) {
			if _, hasDef := spec.KindValueDefs[gn.disc]; !hasDef {
				// A PLUGIN kind (agent/module/package-group/group/…) or an external
				// deploy substrate — validated by the plugin's served schema at
				// runPluginKind / the loader path, not by a kept core value def, so
				// skip it here (the loader parse already hard-rejected any node with NO
				// recognized discriminator).
				continue
			}
			// Validate the node's (desugared) inline discriminator value against its
			// KEPT value def (#CandyValue / #<Kind>Value) — the SAME host-side
			// closedness gate the loader runs (validateKindValueCUE) over the real
			// corpus.
			if verr := validateKindValueCUE(gn); verr != nil {
				t.Errorf("FAIL %s:%s.%s: %v", f, gn.disc, gn.name, verr)
				continue
			}
			counts[gn.disc]++
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
