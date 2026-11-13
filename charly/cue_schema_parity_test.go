package main

// The concat → package cutover's proof (TODO 2.5, D-SCH-2, DESIGN §10.3).
//
// charly used to compile the base document schema as ONE package-less blob — every
// `schema/*.cue` body concatenated via `schemaconcat.ConcatSchema` — and now composes it by
// IMPORT: `github.com/opencharly/spec/schema` as a CUE module instance (cue_schema.go). The two
// forms must be the SAME SCHEMA, and that is asserted here as VALUE EQUALITY — every top-level
// field of one, by name, rendered and compared against the other's — rather than as "a corpus of
// documents validated the same way".
//
// Why equality rather than a corpus: it is practical (both forms compose in under a second) and
// it is strictly STRONGER. Identical values mean identical verdicts on every document, including
// every document no corpus holds; a corpus can only ever sample. The corpus arm is not dropped,
// it is the rest of the suite: `cue_candy_test.go` validates the manifests this repo ships against
// this same composed schema, and the loader tests load the repo's own project through it — so
// `go test ./...` runs the real documents against the new composition.
//
// The comparison is proved SENSITIVE by the second test below: the same helper is handed a
// module instance composed with one schema file removed, and it must report the difference. A
// parity test that cannot fail is not a parity test — and "the package loader silently dropped a
// file" is exactly the failure this cutover has already met once (`schema/_common.cue`, which CUE
// excludes from a package load because of its leading `_`).

import (
	"path/filepath"
	"sort"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/format"

	sdkschema "github.com/opencharly/spec/schema"
	"github.com/opencharly/spec/schemaconcat"
)

// concatBaseSchema composes the base the RETIRED way — the text concatenation charly compiled
// before this cutover — so the change has an oracle it cannot influence. `exclude` is the
// sensitivity arm's hook; pass nil for the real schema.
func concatBaseSchema(t *testing.T, exclude func(string) bool) cue.Value {
	t.Helper()
	body, files, err := schemaconcat.ConcatSchema(sdkschema.FS, ".", exclude)
	if err != nil {
		t.Fatalf("concatenating the spec schema: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("the concatenation read no schema files")
	}
	v := cueSchemaCtx().CompileString(body)
	if err := v.Err(); err != nil {
		t.Fatalf("the concatenated schema does not compile: %v", err)
	}
	return v
}

// baseSchemaFields returns every top-level field of a composed schema by name, rendered to its
// canonical form. Definitions, hidden fields and regular fields are all included (cue.All):
// a difference in ANY of them is a difference in the schema.
func baseSchemaFields(t *testing.T, v cue.Value) map[string]string {
	t.Helper()
	it, err := v.Fields(cue.All())
	if err != nil {
		t.Fatalf("iterating the composed schema's fields: %v", err)
	}
	out := map[string]string{}
	for it.Next() {
		sel := it.Selector()
		rendered, err := format.Node(it.Value().Syntax())
		if err != nil {
			t.Fatalf("rendering %s: %v", sel, err)
		}
		out[sel.String()] = string(rendered)
	}
	return out
}

// baseSchemaDiff returns one line per top-level field whose presence or value differs between two
// composed schemas, sorted. An empty result means the two values carry the same fields with the
// same constraints.
func baseSchemaDiff(t *testing.T, concat, composed cue.Value) []string {
	t.Helper()
	concatFields := baseSchemaFields(t, concat)
	composedFields := baseSchemaFields(t, composed)
	diff := make([]string, 0, len(concatFields))
	for name, concatVal := range concatFields {
		composedVal, ok := composedFields[name]
		switch {
		case !ok:
			diff = append(diff, name+": present in the concatenation, absent from the package")
		case concatVal != composedVal:
			diff = append(diff, name+": the package's value differs from the concatenation's")
		}
	}
	for name := range composedFields {
		if _, ok := concatFields[name]; !ok {
			diff = append(diff, name+": present in the package, absent from the concatenation")
		}
	}
	sort.Strings(diff)
	return diff
}

// TestBaseSchemaImportMatchesConcatenation is THE cutover gate: the schema composed by import and
// the schema the text concatenation produced are the same schema, field for field.
func TestBaseSchemaImportMatchesConcatenation(t *testing.T) {
	old := concatBaseSchema(t, nil)
	composed := sharedCueSchema()

	fields := baseSchemaFields(t, old)
	if len(fields) == 0 {
		t.Fatal("the concatenated base schema carries no top-level fields")
	}
	t.Logf("compared %d top-level fields (definitions, hidden fields and regular fields)", len(fields))
	if diff := baseSchemaDiff(t, old, composed); len(diff) != 0 {
		t.Fatalf("the base schema composed by IMPORT differs from the concatenation it replaced (%d difference(s)) — the module instance is NOT the same schema the runtime validated against before:\n  %v",
			len(diff), diff)
	}
}

// TestBaseSchemaParityDetectsAMissingFile proves the comparison above can FAIL, and does so on the
// failure this cutover is most exposed to: the package loader silently excluding a file. The
// module instance is composed with `docs.cue` removed from the overlay and compared against the
// full concatenation; the difference must be reported.
func TestBaseSchemaParityDetectsAMissingFile(t *testing.T) {
	const dropped = "docs.cue"

	overlay, err := specModuleOverlay()
	if err != nil {
		t.Fatalf("assembling the spec module overlay: %v", err)
	}
	key := filepath.Join(specModuleRoot, "schema", dropped)
	if _, ok := overlay[key]; !ok {
		t.Fatalf("the overlay carries no %s — the sensitivity arm would compare a full schema with itself", key)
	}
	delete(overlay, key)

	partial, err := buildSpecModuleInstance(overlay)
	if err != nil {
		t.Fatalf("composing the module instance without %s: %v", dropped, err)
	}
	diff := baseSchemaDiff(t, concatBaseSchema(t, nil), partial)
	if len(diff) == 0 {
		t.Fatalf("removing %s from the module overlay produced NO difference — the parity comparison is blind to a file the composition lost, so it cannot prove anything", dropped)
	}
	t.Logf("the comparison reports %d difference(s) once %s is removed (first: %s)", len(diff), dropped, diff[0])
}

// TestSpecModuleOverlayIsAModule gates the shape cue_schema.go requires of the two embedded trees:
// the package source lands under <root>/schema/ and the module declaration at
// <root>/cue.mod/module.cue, which is the ONLY path CUE's loader reads a module from.
func TestSpecModuleOverlayIsAModule(t *testing.T) {
	overlay, err := specModuleOverlay()
	if err != nil {
		t.Fatalf("assembling the spec module overlay: %v", err)
	}
	if _, ok := overlay[filepath.Join(specModuleRoot, "cue.mod", "module.cue")]; !ok {
		t.Fatal("the overlay carries no cue.mod/module.cue — without it CUE loads the schema as a bare package and the module's language.version never governs evaluation")
	}
	var schemaFiles int
	for p := range overlay {
		if filepath.Dir(p) == filepath.Join(specModuleRoot, "schema") {
			schemaFiles++
		}
	}
	if schemaFiles == 0 {
		t.Fatal("the overlay carries no schema/*.cue files")
	}
}
