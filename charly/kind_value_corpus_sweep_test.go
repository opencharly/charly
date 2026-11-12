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
// SELECTION follows the PRODUCTION resolver. A body is judged when the route the loader uses
// answers with a `#<Kind>Value` def name — the provider's declared InputDef first, the base's
// spec.KindValueDefs as the compatibility fallback (kindValueDefPath) — and the provider is
// connected the way the loader connects a referenced plugin kind (gatedKindProvider). That is
// what keeps a moved kind in the sweep: `kind: pipeline`'s body is plugin-pipeline's CUE now,
// so it has no base entry left, and a selection keyed on spec.KindValueDefs alone would drop
// its bodies silently. The `base` arm remains the pre-change oracle for the BASE source and
// skips — visibly, and counted — a def the base no longer declares.
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

// gatedKindProvider resolves the provider that serves `disc`, through the SAME route the gate
// uses: the registry first and, when `connect` is set, the on-demand connect
// (`connectPluginByWord`) the loader uses for a referenced plugin kind — it host-builds and
// registers that plugin's unit. This is what lets a corpus witness see a PLUGIN-DECLARED gate
// def: `kind: pipeline`'s body lives in plugin-pipeline's CUE module and its `kind` capability
// declares `InputDef: "#PipelineValue"`, so its provider is the only thing that can name the def.
//
// `connect` is false for the DEFAULT-suite corpus test (a plain `go test ./...` must never
// build a plugin or hit the network), true for the opt-in sweep. A miss is cached only when a
// connect was actually attempted, so the default suite cannot poison the sweep's resolution.
var gatedKindProviders = map[string]Provider{}

func gatedKindProvider(disc string, connect bool) (Provider, bool) {
	if p, seen := gatedKindProviders[disc]; seen {
		return p, p != nil
	}
	p, ok := providerRegistry.ResolveKind(disc)
	if !ok && connect {
		p, ok = connectPluginByWord(ClassKind, disc)
		if !ok {
			gatedKindProviders[disc] = nil
			return nil, false
		}
	}
	if !ok {
		return nil, false
	}
	gatedKindProviders[disc] = p
	return p, true
}

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

// TestSweepSelectionResolvesAPluginDeclaredGateDef is the sweep's own R7 witness for the
// selection: a kind whose `#<Kind>Value` def exists ONLY in a served plugin unit must still be
// selected, and the def path must come from the provider's DECLARATION rather than the base map.
//
// The fixture is unit 0's `gateprobe` unit (a def the base does not declare) registered into the
// real provider registry, so this drives the production resolver — `gatedKindProvider` plus
// `kindValueDefPath` — with no connect attempt (the registry hit is the whole resolution).
func TestSweepSelectionResolvesAPluginDeclaredGateDef(t *testing.T) {
	if _, inBase := spec.KindValueDefs["gateprobe"]; inBase {
		t.Fatal("the BASE already declares gateprobe — this fixture no longer discriminates")
	}
	prov := gateProbeUnit(t)
	if err := providerRegistry.register(prov, "test"); err != nil {
		t.Fatalf("register the gateprobe provider: %v", err)
	}

	got, ok := gatedKindProvider("gateprobe", false)
	if !ok {
		t.Fatal("the sweep's resolver did not find the registered kind provider")
	}
	defPath, gated := kindValueDefPath(got, "gateprobe")
	if !gated {
		t.Fatalf("a kind whose def is declared by a served unit must be GATED by the sweep's selection; got none")
	}
	if defPath != "#GateprobeValue" {
		t.Fatalf("def path = %q, want the provider's declared %q", defPath, "#GateprobeValue")
	}
	// The def must then RESOLVE through the composed unit, which is what makes the body judgeable.
	if _, err := kindValueDefValue(defPath); err != nil {
		t.Fatalf("the declared def does not resolve in the composed unit: %v", err)
	}
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
	parseErrs, judged, baseSkips := 0, 0, 0
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
			// SELECTION follows the PRODUCTION resolver, not the base map: a kind is gated
			// when the route the loader uses answers with a `#<Kind>Value` def name —
			// the provider's declared InputDef first, the base's spec.KindValueDefs as the
			// compatibility fallback (kindValueDefPath). A kind whose body moved into its
			// owning plugin's CUE module has NO base entry any more; resolving it through
			// spec.KindValueDefs alone would silently drop every one of its bodies from the
			// sweep, which is exactly the loss this selection exists to prevent.
			prov, _ := gatedKindProvider(pn.Disc, true)
			if _, gated := kindValueDefPath(prov, pn.Disc); !gated {
				continue
			}
			verdict := "OK"
			if mode == "base" {
				verdict = baseOnlyKindValueVerdict(pn)
				if verdict == "SKIP" {
					// The base arm is the PRE-CHANGE oracle: it resolves the def in the base
					// alone, so a kind whose def has MOVED out of the base cannot be judged
					// through it and is skipped VISIBLY (the composed arm, which is the
					// production gate, still judges it).
					baseSkips++
					continue
				}
			} else {
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
	t.Logf("gate=%s: judged %d entity bodies (%d files unparsable, %d bodies the base arm cannot judge) -> %s", mode, judged, parseErrs, baseSkips, outPath)
	for _, k := range kinds {
		t.Logf("  %-12s %d", k, counts[k])
	}
}
