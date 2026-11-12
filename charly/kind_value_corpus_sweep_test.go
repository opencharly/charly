package main

// kind_value_corpus_sweep_test.go — the kind-value gate's REAL-CORPUS sweep (opt-in).
//
// The gate resolves every host-value-gated entity's #<Kind>Value def, and that resolution moved
// from the base schema alone to the COMPOSED unit (base & every served plugin unit) plus the
// provider's own declaration. This sweep is the behaviour-preservation witness over real authored
// documents rather than fixtures: it walks a materialized umbrella checkout, judges EVERY entity
// body of every gated kind through the production gate, and writes one deterministic verdict line
// per body, so the SAME corpus can be judged through either schema source and the two verdict
// lists diffed.
//
//	CHARLY_KIND_VALUE_GATE=base       the PRE-CHANGE gate: the def looked up in sharedCueSchema()
//	CHARLY_KIND_VALUE_GATE=composed   the production gate (the default)
//
// The `base` arm is a test-local ORACLE for the old schema source — it reproduces the old lookup
// exactly and is never production code; it exists so "before vs after" is two runs of ONE file on
// ONE tree, not two trees that could differ in something else.
//
// Opt-in because the corpus lives in the umbrella, not in this repo: with CHARLY_CORPUS_ROOT
// unset the test SKIPS VISIBLY, the same contract the box corpus tests use. CHARLY_CORPUS_OUT
// chooses where the verdict list lands (default: <tmp>/kind-value-corpus.txt).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// corpusNodes parses a YAML stream the way the loader does (the active loader parser, so plugin
// verb sugar is desugared in place) and returns every entity node, or an error when the file is
// not a charly project (a CI workflow, a compose file, …) and is therefore out of scope.
func corpusNodes(data []byte) ([]spec.ParsedNode, error) {
	var all []spec.ParsedNode
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return all, nil
			}
			return nil, err
		}
		_, pp, err := activeLoaderParser.ParseDoc(&doc, loaderThreaded())
		if err != nil {
			return nil, err
		}
		all = append(all, pp.Nodes...)
	}
}

// baseOnlyKindValueVerdict reproduces the PRE-CHANGE gate exactly: the def is looked up in the
// base schema ALONE (sharedCueSchema), never in the composed unit. It is the sweep's `base` arm —
// a regression oracle, not a second implementation of the production path.
func baseOnlyKindValueVerdict(pn spec.ParsedNode) string {
	var body map[string]json.RawMessage
	if len(pn.Body) == 0 || json.Unmarshal([]byte(pn.Body), &body) != nil {
		return "SKIP"
	}
	defPath, ok := spec.KindValueDefs[pn.Disc]
	if !ok {
		return "SKIP"
	}
	def := sharedCueSchema().LookupPath(cue.ParsePath(defPath))
	if def.Err() != nil {
		return "FAIL " + strings.Join(strings.Fields(fmt.Sprintf("kind value def %s not found: %v", defPath, def.Err())), " ")
	}
	bodyJSON, err := requireProjectLoader().EntityBodyJSON(pn)
	if err != nil {
		return "FAIL " + strings.Join(strings.Fields(err.Error()), " ")
	}
	entity, err := requireProjectLoader().CueDocFromJSON("node "+pn.Name, bodyJSON)
	if err != nil {
		return "FAIL " + strings.Join(strings.Fields(err.Error()), " ")
	}
	if verr := entity.Unify(def).Validate(); verr != nil {
		// The SAME diagnostic rendering the gate uses (cue/errors.Details), so the two arms
		// differ only in the schema SOURCE — never in how a rejection is worded.
		return "FAIL " + strings.Join(strings.Fields(fmt.Sprintf("%s: %s", pn.Disc, cueerrors.Details(verr, nil))), " ")
	}
	return "OK"
}

// TestKindValueCorpusSweep judges every real entity body of a host-value-gated kind through the
// gate and writes the verdict list. Run it twice (CHARLY_KIND_VALUE_GATE=base and
// =composed) and diff the two files: a non-empty diff is a moved verdict.
func TestKindValueCorpusSweep(t *testing.T) {
	root := os.Getenv("CHARLY_CORPUS_ROOT")
	if root == "" {
		t.Skip("CHARLY_CORPUS_ROOT unset — the umbrella corpus is not present in this checkout")
	}
	mode := os.Getenv("CHARLY_KIND_VALUE_GATE")
	if mode == "" {
		mode = "composed"
	}
	if mode != "base" && mode != "composed" {
		t.Fatalf("CHARLY_KIND_VALUE_GATE=%q: want base or composed", mode)
	}
	if err := loadBuiltinPluginUnits(); err != nil {
		t.Fatal(err)
	}

	var files []string
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".build", ".worktrees", ".check", "node_modules", "tmp", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		n := d.Name()
		if strings.HasSuffix(n, ".yml") || strings.HasSuffix(n, ".yaml") {
			files = append(files, p)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	sort.Strings(files)

	rel := func(p string) string {
		r, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return p
		}
		return r
	}

	var out strings.Builder
	parseErrs, judged := 0, 0
	counts := map[string]int{}
	for _, f := range files {
		data, rerr := os.ReadFile(f)
		if rerr != nil {
			parseErrs++
			fmt.Fprintf(&out, "PARSE-ERR %s\n", rel(f))
			continue
		}
		// Register a file's declared external words so a deploy/bed using one parses as an
		// entity (the same pre-scan the real load runs).
		prescanDeclaredPluginWords(data, filepath.Dir(f))
		nodes, perr := corpusNodes(data)
		if perr != nil {
			parseErrs++
			fmt.Fprintf(&out, "PARSE-ERR %s\n", rel(f))
			continue
		}
		for _, pn := range nodes {
			// SELECTION is the pre-change set so both arms judge the same bodies; the arm only
			// changes WHERE the def is resolved (base alone vs the composed unit).
			if _, gated := spec.KindValueDefs[pn.Disc]; !gated {
				continue
			}
			verdict := "OK"
			if mode == "base" {
				verdict = baseOnlyKindValueVerdict(pn)
				if verdict == "SKIP" {
					continue
				}
			} else {
				prov, _ := providerRegistry.ResolveKind(pn.Disc)
				if verr := validateKindValueCUE(pn, prov); verr != nil {
					verdict = "FAIL " + strings.Join(strings.Fields(verr.Error()), " ")
				}
			}
			judged++
			counts[pn.Disc]++
			fmt.Fprintf(&out, "%s|%s|%s|%s\n", rel(f), pn.Disc, pn.Name, verdict)
		}
	}

	outPath := os.Getenv("CHARLY_CORPUS_OUT")
	if outPath == "" {
		outPath = filepath.Join(os.TempDir(), "kind-value-corpus-"+mode+".txt")
	}
	if werr := os.WriteFile(outPath, []byte(out.String()), 0o644); werr != nil {
		t.Fatal(werr)
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	t.Logf("gate=%s: judged %d entity bodies (%d files unparsable) -> %s", mode, judged, parseErrs, outPath)
	for _, k := range kinds {
		t.Logf("  %-12s %d", k, counts[k])
	}
}
