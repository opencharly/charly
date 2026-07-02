// Command schemagen is the dev-time companion code generator for the `spec`
// package. It is invoked ONLY by `task cue:gen` (never at runtime) and has two
// modes:
//
//	-mode=concat -out=<file>   Concatenate charly/schema/*.cue into one file
//	                           headed `package spec` + the file-level `@go(spec)`
//	                           attribute, ready for `cue exp gengotypes`.
//	-mode=vocab  -out=<file>   Compile the concatenation via the embedded
//	                           cuelang.org/go API and emit charly/spec/vocab_gen.go
//	                           — the single-source vocabulary lists (kind keywords,
//	                           document directives, step keywords, contexts, the
//	                           flat #Op verb/modifier field set, and the live-verb
//	                           method allowlists).
//
// CONCATENATION CONTRACT (R3): concatSchema replicates EXACTLY the mechanism the
// runtime uses in charly/cue_schema.go `sharedCueSchema` — every package-less
// schema/*.cue file, sorted by name, joined with a trailing newline. The runtime
// reads the files from `//go:embed`; this tool reads them from disk (it runs at
// dev time with the working tree present). If you change the runtime
// concatenation order, change it here too — the two MUST stay byte-identical or
// the generated Go types drift from what the runtime validates.
//
// PARAM-GEN SCOPE (the two modes diverge by INPUT, not by mechanism): the
// `vocab` mode reads the FULL schema (every schema/*.cue) — it needs #Node's
// arms (node.cue) to derive KindWords, so its concatenation stays byte-identical
// to the runtime `sharedCueSchema`. The `concat` mode (which feeds `cue exp
// gengotypes` → the Go param structs) reads the schema MINUS node.cue: it
// defines the node-disjunction wrappers (#Node/#NodeDoc/#*Arm/#*Value), which
// gengotypes degrades to `map[string]any` and which are NOT charly param structs.
// (The egress validation schemas moved to candy/plugin-egress in M16, so they no
// longer live here to exclude.) The entity defs (#Box/#Deploy/#Op/#Vm/…) do not
// reference node.cue, so the exclusion is compile-clean. Both modes share the ONE concatSchema helper
// (R3); only the file filter differs.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"regexp"
	"sort"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"

	"github.com/opencharly/charly/charly/internal/schemaconcat"
)

func main() {
	mode := flag.String("mode", "", "concat | vocab | version | retag")
	schemaDir := flag.String("schema", "charly/schema", "path to the schema/*.cue directory")
	pkg := flag.String("pkg", "spec", "Go package for the concat header (spec | params)")
	out := flag.String("out", "", "output file path")
	flag.Parse()

	if *out == "" {
		fatal("schemagen: -out is required")
	}
	switch *mode {
	case "concat":
		if err := writeConcat(*schemaDir, *out, *pkg); err != nil {
			fatal("schemagen concat: %v", err)
		}
	case "vocab":
		if err := writeVocab(*schemaDir, *out); err != nil {
			fatal("schemagen vocab: %v", err)
		}
	case "version":
		if err := writeVersion(*schemaDir, *out); err != nil {
			fatal("schemagen version: %v", err)
		}
	case "retag":
		if err := retagFile(*out); err != nil {
			fatal("schemagen retag: %v", err)
		}
	default:
		fatal("schemagen: -mode must be concat, vocab, version, or retag (got %q)", *mode)
	}
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

// excludeParamGen reports whether a schema/*.cue file is EXCLUDED from the
// param-gen concatenation (the gengotypes input). node.cue defines the
// node-disjunction wrappers — not a charly param struct, and gengotypes degrades
// it to `map[string]any`. See the package doc PARAM-GEN SCOPE note. The vocab
// mode passes `nil` (no exclusion) so #Node's arms are present for KindWords.
// (The egress validation schemas moved to candy/plugin-egress in M16, so there
// are no egress_*.cue files here to exclude.)
func excludeParamGen(name string) bool {
	// node.cue: the node-disjunction wrappers (degrade to map[string]any).
	// migration.cue: the #Migration validation-only defs — not param structs
	// (the charly migrate engine hand-decodes the table; see migration.cue).
	return name == "node.cue" || name == "migration.cue"
}

// concatSchema delegates to schemaconcat.ConcatSchema — the SINGLE schema-concatenation
// contract shared with the runtime `sharedCueSchema` in charly/cue_schema.go
// (R3). The generator reads the working-tree files from disk (os.DirFS); the
// runtime reads its `//go:embed` FS; both feed the same helper, so the compiled
// schema and the generated Go types can never drift. A nil exclude includes
// every file (the full-schema concatenation the runtime uses).
func concatSchema(dir string, exclude func(name string) bool) (string, []string, error) {
	return schemaconcat.ConcatSchema(os.DirFS(dir), ".", exclude)
}

// specSource returns the concatenation headed with the `package <pkg>` clause and
// the file-level `@go(<pkg>)` attribute — what `cue exp gengotypes` consumes to
// emit that Go package. pkg is "spec" for the core schema and "params" for a
// plugin's self-contained schema (same one concatenation contract — R3). The cue
// API (vocab mode) compiles the same string. The exclude filter scopes the
// param-gen input (see excludeParamGen).
func specSource(dir, pkg string, exclude func(name string) bool) (string, error) {
	body, _, err := concatSchema(dir, exclude)
	if err != nil {
		return "", err
	}
	return "package " + pkg + "\n\n@go(" + pkg + ")\n\n" + body, nil
}

// ----------------------------------------------------------------------------
// retag mode — the Go-native yaml-tag doubling (replaces the former cue:gen sed)
// ----------------------------------------------------------------------------

// reJSONOnlyTag matches a struct field tag literal carrying ONLY a json tag,
// `json:"X"` (backtick-delimited — gengotypes emits json tags only). retag doubles
// it with a matching yaml tag so charly's yaml.v3 round-trip (saveDeployState, the
// deploy-overlay merge, charly.yml read/write) keys off the SAME wire key — yaml.v3
// otherwise lowercases the Go field name and silently drops every snake_case key.
var reJSONOnlyTag = regexp.MustCompile("`json:\"([^\"]*)\"`")

// reBareYamlKey matches a yaml tag whose value is a bare key (alpha first char, no
// comma/quote). retag appends ,omitempty so a zero value drops out and the CUE
// default re-applies (parity with the former hand structs — e.g. an empty
// firmware:"" would otherwise break the `*"bios"|…` default). A key that already
// carries a comma (an existing ,omitempty) is left untouched.
var reBareYamlKey = regexp.MustCompile(`yaml:"([a-zA-Z][^",]*)"`)

// retagFile rewrites the gengotypes-generated Go file in place: (1) double every
// json-only struct tag with a matching yaml tag, then (2) append ,omitempty to
// every bare yaml key. This is the principled Go-native replacement for the former
// cue:gen `sed -i` steps — a compiled, documented, idempotent transform that lives
// INSIDE schemagen (never sed on generated Go). The two substitutions are exactly
// the former sed expressions, so the committed cue_types_gen.go is byte-identical;
// gofmt (the next cue:gen step) realigns the tag columns. Idempotent: a fresh
// gengotypes file carries json-only tags, and a re-run finds no json-only tag to
// double and every yaml key already carrying ,omitempty.
func retagFile(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out := reJSONOnlyTag.ReplaceAll(src, []byte("`yaml:\"${1}\" json:\"${1}\"`"))
	out = reBareYamlKey.ReplaceAll(out, []byte(`yaml:"${1},omitempty"`))
	return os.WriteFile(path, out, 0o644)
}

// writeConcat emits the gengotypes input — the PARAM-GEN-scoped concatenation
// (node.cue excluded; see excludeParamGen) headed with `package pkg`.
func writeConcat(dir, out, pkg string) error {
	src, err := specSource(dir, pkg, excludeParamGen)
	if err != nil {
		return err
	}
	return os.WriteFile(out, []byte(src), 0o644)
}

// ----------------------------------------------------------------------------
// vocab mode
// ----------------------------------------------------------------------------

func writeVocab(dir, out string) error {
	// FULL schema (nil exclude): the vocab generator needs #Node's arms
	// (node.cue) to derive KindWords, so this concatenation matches the runtime
	// sharedCueSchema (every schema/*.cue).
	src, err := specSource(dir, "spec", nil)
	if err != nil {
		return err
	}
	ctx := cuecontext.New()
	schema := ctx.CompileString(src)
	if schema.Err() != nil {
		return fmt.Errorf("compile schema: %v", errors.Details(schema.Err(), nil))
	}

	kinds, err := nodeDiscriminators(schema)
	if err != nil {
		return err
	}
	resourceKinds, err := enumValues(schema, "#ResourceKind")
	if err != nil {
		return err
	}
	directives, err := fieldLabels(schema, "#NodeDoc")
	if err != nil {
		return err
	}
	opFields, err := fieldLabels(schema, "#Op")
	if err != nil {
		return err
	}
	stepKeywords, err := stepKeywordLabels(schema, opFields)
	if err != nil {
		return err
	}
	contexts, err := enumValues(schema, "#Context")
	if err != nil {
		return err
	}
	opVerbs, err := enumValues(schema, "#OpVerb")
	if err != nil {
		return err
	}
	// AuthoringVerbs — the AUTHORABLE #Op field vocabulary: every #Op field MINUS
	// the runtime-derived fields that are never authored (origin is OCI-label
	// reporting state; venue is stamped from a step's bundle-tree position;
	// intent_do is stamped from the step keyword). The #Step arms forbid venue +
	// intent_do, and origin is yaml:"-" in Go — none is an authoring surface.
	authoringVerbs := excludeFrom(opFields, opRuntimeDerivedFields)

	// DataKeys — the DATA discriminator keywords: the non-scalar fields of the
	// authorable entity defs (#Candy + #Deploy) that the node-form parser SETS as a
	// CHILD node onto the owning entity. Candy contributes every non-scalar field;
	// #Deploy contributes every non-scalar field EXCEPT the inline-tolerated +
	// loader-derived set (see deployInlineNonScalarFields). `plan` is excluded
	// everywhere — plan steps are CHILD step nodes, not a data field.
	candyData, err := nonScalarFieldLabels(schema, "#Candy", map[string]bool{"plan": true})
	if err != nil {
		return err
	}
	deployExclude := map[string]bool{"plan": true}
	for _, f := range deployInlineNonScalarFields {
		deployExclude[f] = true
	}
	deployData, err := nonScalarFieldLabels(schema, "#Deploy", deployExclude)
	if err != nil {
		return err
	}
	dataKeys := unionSorted(candyData, deployData)

	code := renderVocab(vocabSets{
		kinds:          kinds,
		resourceKinds:  resourceKinds,
		directives:     directives,
		stepKeywords:   stepKeywords,
		contexts:       contexts,
		dataKeys:       dataKeys,
		opFields:       opFields,
		opVerbs:        opVerbs,
		authoringVerbs: authoringVerbs,
	})
	formatted, err := format.Source([]byte(code))
	if err != nil {
		return fmt.Errorf("gofmt generated vocab: %w\n%s", err, code)
	}
	return os.WriteFile(out, formatted, 0o644)
}

// opRuntimeDerivedFields are the #Op fields that are RUNTIME-DERIVED, never
// authored — excluded from AuthoringVerbs. origin is OCI-label reporting state
// (yaml:"-"); venue is stamped from a step's bundle-tree position; intent_do is
// stamped from the step keyword. The #Step arms forbid venue + intent_do
// (`venue?: _|_, intent_do?: _|_`), so this list mirrors a CUE-declared fact.
var opRuntimeDerivedFields = []string{"origin", "venue", "intent_do"}

// deployInlineNonScalarFields are the NON-SCALAR #Deploy fields the node-form
// parser TOLERATES inline in the bundle value rather than folding into a CHILD
// node — so they are NOT DataKeys. Two reasons a field lands here:
//   - loader-derived / machine-persisted runtime state, authored inline by
//     saveDeployState's explodeFields default arm (resolved_port, vm_state) or
//     forbidden by #BundleValue and rebuilt from tree position (nested, peer);
//   - legacy inline-tolerated authoring (sidecar — the common tailscale sidecar
//     map is persisted inline; kubernetes/resources/expose/storage/probes).
//
// Adding `<x>` here keeps a non-scalar #Deploy field inline-legal; omitting a NEW
// non-scalar field makes it a child-node DataKey (the node-form "everything is a
// node" default). Behavior-preserving: this list is exactly the gap between
// #Deploy's non-scalar fields and the former hand nodeDataKeys.
var deployInlineNonScalarFields = []string{
	"sidecar", "kubernetes", "resources", "expose", "storage", "probes",
	"resolved_port", "vm_state", "nested", "peer",
}

// nonScalarFieldLabels returns the sorted field labels of a struct def whose
// VALUE is non-scalar — its incomplete kind includes StructKind or ListKind (a
// list, struct, or map). These are the fields that, in node-form, fold into a
// CHILD node. A field in `exclude` is skipped; a bottom (_|_) field is skipped.
func nonScalarFieldLabels(schema cue.Value, def string, exclude map[string]bool) ([]string, error) {
	v := schema.LookupPath(cue.ParsePath(def))
	if v.Err() != nil {
		return nil, fmt.Errorf("%s not found: %w", def, v.Err())
	}
	it, err := v.Fields(cue.Optional(true), cue.Definitions(false))
	if err != nil {
		return nil, fmt.Errorf("%s fields: %w", def, err)
	}
	seen := map[string]bool{}
	for it.Next() {
		label := it.Selector().Unquoted()
		if exclude[label] {
			continue
		}
		fv := it.Value()
		if fv.Err() != nil {
			continue // bottom (_|_) — a forbidden / unsatisfiable field
		}
		if fv.IncompleteKind()&(cue.StructKind|cue.ListKind) != 0 {
			seen[label] = true
		}
	}
	return sortedKeys(seen), nil
}

// excludeFrom returns vals with every name in exclude removed (order preserved).
func excludeFrom(vals []string, exclude []string) []string {
	drop := map[string]bool{}
	for _, e := range exclude {
		drop[e] = true
	}
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if !drop[v] {
			out = append(out, v)
		}
	}
	return out
}

// unionSorted returns the sorted union of two label slices.
func unionSorted(a, b []string) []string {
	seen := map[string]bool{}
	for _, x := range a {
		seen[x] = true
	}
	for _, x := range b {
		seen[x] = true
	}
	return sortedKeys(seen)
}

// nodeDiscriminators returns the kind keywords — the concrete discriminator key
// of each arm of the #Node disjunction (box / candy / bundle / …), sorted.
func nodeDiscriminators(schema cue.Value) ([]string, error) {
	node := schema.LookupPath(cue.ParsePath("#Node"))
	if node.Err() != nil {
		return nil, fmt.Errorf("#Node not found: %w", node.Err())
	}
	// #Node is USUALLY a disjunction of per-kind arms (Expr → OrOp + the arms). When only
	// ONE core kind arm remains (C2-substrate reduced #Node to just #CandyArm), Expr is NOT an
	// OrOp, so treat the node ITSELF as the single arm — its regular fields are the (one)
	// discriminator. Without this, a single-arm #Node yields an EMPTY KindWords.
	op, args := node.Expr()
	if op != cue.OrOp {
		args = []cue.Value{node}
	}
	seen := map[string]bool{}
	for _, arm := range args {
		it, err := arm.Fields(cue.Optional(true), cue.Definitions(false))
		if err != nil {
			continue // a non-struct arm has no discriminator
		}
		for it.Next() {
			seen[it.Selector().Unquoted()] = true
		}
	}
	return sortedKeys(seen), nil
}

// fieldLabels returns the sorted regular (non-pattern) field labels of a struct
// def — used for #Op (verb/modifier vocabulary) and #NodeDoc (directives).
func fieldLabels(schema cue.Value, def string) ([]string, error) {
	v := schema.LookupPath(cue.ParsePath(def))
	if v.Err() != nil {
		return nil, fmt.Errorf("%s not found: %w", def, v.Err())
	}
	it, err := v.Fields(cue.Optional(true), cue.Definitions(false))
	if err != nil {
		return nil, fmt.Errorf("%s fields: %w", def, err)
	}
	seen := map[string]bool{}
	for it.Next() {
		seen[it.Selector().Unquoted()] = true
	}
	return sortedKeys(seen), nil
}

// stepKeywordLabels returns the step intent keywords — the labels that appear in
// some #Step arm but are NOT #Op fields (run / check / agent-run / … / include).
func stepKeywordLabels(schema cue.Value, opFields []string) ([]string, error) {
	opSet := map[string]bool{}
	for _, f := range opFields {
		opSet[f] = true
	}
	step := schema.LookupPath(cue.ParsePath("#Step"))
	if step.Err() != nil {
		return nil, fmt.Errorf("#Step not found: %w", step.Err())
	}
	_, args := step.Expr()
	seen := map[string]bool{}
	for _, arm := range args {
		it, err := arm.Fields(cue.Optional(true), cue.Definitions(false))
		if err != nil {
			continue
		}
		for it.Next() {
			label := it.Selector().Unquoted()
			if !opSet[label] {
				seen[label] = true
			}
		}
	}
	return sortedKeys(seen), nil
}

// enumValues returns the string-literal arms of a pure string-disjunction def
// (#CdpMethod / #Context / …), in CUE source order (NOT sorted — the source order
// is the meaningful order for an enum).
func enumValues(schema cue.Value, def string) ([]string, error) {
	v := schema.LookupPath(cue.ParsePath(def))
	if v.Err() != nil {
		return nil, fmt.Errorf("%s not found: %w", def, v.Err())
	}
	_, args := v.Expr()
	if len(args) == 0 {
		// A single-value "enum" is a plain string literal (no disjunction).
		if s, err := v.String(); err == nil {
			return []string{s}, nil
		}
		return nil, fmt.Errorf("%s is not a string enum", def)
	}
	out := make([]string, 0, len(args))
	for _, a := range args {
		s, err := a.String()
		if err != nil {
			return nil, fmt.Errorf("%s arm is not a string literal: %w", def, err)
		}
		out = append(out, s)
	}
	return out, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// vocabSets bundles every CUE-derived word list renderVocab emits.
type vocabSets struct {
	kinds          []string
	resourceKinds  []string
	directives     []string
	stepKeywords   []string
	contexts       []string
	dataKeys       []string
	opFields       []string
	opVerbs        []string
	authoringVerbs []string
}

func renderVocab(s vocabSets) string {
	var b bytes.Buffer
	b.WriteString("// Code generated by `task cue:gen` (charly/internal/schemagen); DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// The single-source vocabulary, derived from charly/schema/*.cue. These are\n")
	b.WriteString("// the SAME word lists the runtime validates against — never hand-maintain a\n")
	b.WriteString("// parallel copy; regenerate with `task cue:gen`.\n\n")
	b.WriteString("package spec\n\n")

	writeStrSlice(&b, "KindWords", "the reserved kind keywords (the #Node disjunction discriminators).", s.kinds)
	writeStrSlice(&b, "ResourceKinds", "the DEPLOYABLE subset of the kind keywords — the kinds whose #Node arm nests a sub-ENTITY (resource) child (#ResourceKind).", s.resourceKinds)
	writeStrSlice(&b, "DocDirectives", "the reserved document directives (#NodeDoc top-level keys).", s.directives)
	writeStrSlice(&b, "StepKeywords", "the plan-step intent keywords (#Step arms minus #Op fields).", s.stepKeywords)
	writeStrSlice(&b, "ContextWords", "the plan-step execution contexts (#Context).", s.contexts)
	writeStrSlice(&b, "DataKeys", "the DATA discriminator keywords — the non-scalar #Candy/#Deploy fields the node-form parser folds in as a CHILD node (excludes plan + the inline-tolerated/loader-derived #Deploy non-scalars).", s.dataKeys)
	writeStrSlice(&b, "OpFields", "every #Op verb/modifier field name (the flat Op vocabulary).", s.opFields)
	writeStrSlice(&b, "OpVerbs", "the verb DISCRIMINATOR vocabulary (#OpVerb) — the exactly-one-set verb subset of #Op fields (Op.Kind() + the VerbCatalog dispatch table gate against it).", s.opVerbs)
	writeStrSlice(&b, "AuthoringVerbs", "the AUTHORABLE #Op field vocabulary (#Op fields minus the runtime-derived origin/venue/intent_do).", s.authoringVerbs)
	return b.String()
}

func writeStrSlice(b *bytes.Buffer, name, doc string, vals []string) {
	fmt.Fprintf(b, "// %s is %s\n", name, doc)
	fmt.Fprintf(b, "var %s = []string{\n", name)
	for _, v := range vals {
		fmt.Fprintf(b, "\t%q,\n", v)
	}
	b.WriteString("}\n\n")
}

// ----------------------------------------------------------------------------
// version mode — emit spec.SchemaVersion / spec.SchemaFloor from version.cue
// ----------------------------------------------------------------------------

// writeVersion compiles the FULL schema (nil exclude, like vocab mode) and emits
// charly/spec/version_gen.go with the SchemaVersion + SchemaFloor consts read from
// the CUE-owned #SchemaVersion / #SchemaFloor. This makes charly/schema/version.cue
// the SINGLE source of truth for the schema HEAD; kit.LatestSchemaVersion() parses
// the emitted const. The strict #CanonCalVer regex makes a non-canonical literal
// fail schema COMPILATION here — before any Go is generated.
func writeVersion(dir, out string) error {
	src, err := specSource(dir, "spec", nil)
	if err != nil {
		return err
	}
	ctx := cuecontext.New()
	schema := ctx.CompileString(src)
	if schema.Err() != nil {
		return fmt.Errorf("compile schema: %v", errors.Details(schema.Err(), nil))
	}
	sv, err := stringConst(schema, "#SchemaVersion")
	if err != nil {
		return err
	}
	sf, err := stringConst(schema, "#SchemaFloor")
	if err != nil {
		return err
	}
	code := renderVersion(sv, sf)
	formatted, err := format.Source([]byte(code))
	if err != nil {
		return fmt.Errorf("gofmt generated version: %w\n%s", err, code)
	}
	return os.WriteFile(out, formatted, 0o644)
}

// stringConst returns the concrete string value of a string-constant def
// (#SchemaVersion / #SchemaFloor); errors if missing or not concrete.
func stringConst(schema cue.Value, def string) (string, error) {
	v := schema.LookupPath(cue.ParsePath(def))
	if v.Err() != nil {
		return "", fmt.Errorf("%s not found: %w", def, v.Err())
	}
	s, err := v.String()
	if err != nil {
		return "", fmt.Errorf("%s is not a concrete string: %w", def, err)
	}
	return s, nil
}

func renderVersion(schemaVersion, schemaFloor string) string {
	var b bytes.Buffer
	b.WriteString("// Code generated by `task cue:gen` (charly/internal/schemagen); DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// The HEAD schema CalVer + the oldest migratable floor, derived from\n")
	b.WriteString("// charly/schema/version.cue. kit.LatestSchemaVersion()/SchemaFloor() parse\n")
	b.WriteString("// these; never hand-edit — regenerate with `task cue:gen`.\n\n")
	b.WriteString("package spec\n\n")
	b.WriteString("// SchemaVersion is the HEAD schema CalVer (charly/schema/version.cue #SchemaVersion).\n")
	fmt.Fprintf(&b, "const SchemaVersion = %q\n\n", schemaVersion)
	b.WriteString("// SchemaFloor is the oldest schema version `charly migrate` can migrate FROM\n")
	b.WriteString("// (charly/schema/version.cue #SchemaFloor).\n")
	fmt.Fprintf(&b, "const SchemaFloor = %q\n", schemaFloor)
	return b.String()
}
