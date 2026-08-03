package main

// CUE-validation Core. One compiled schema instance (every schema *.cue file
// from the SDK's schema package unified — shared #Step lives once in
// _common.cue, R3), a kind registry populated by each cue_kind_<name>.go via
// init(), and a per-entity validator. Per-entity validation extracts an entity
// (the `candy:` value of a legacy kind-keyed file, or each value of a
// `pod:`/`k8s:`/… collection map) and unifies it with #<Kind>; a unified
// node-form document is validated whole against #NodeDoc — the sole load gate.
// The legacy shape-routing + hand-written validators are deleted; CUE is the
// single schema source, and it travels WITH the spec module (github.com/opencharly/spec
// owns schema + the generated spec types).

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"

	sdkschema "github.com/opencharly/spec/schema"
	"github.com/opencharly/spec/schemaconcat"
	"github.com/opencharly/spec/spec"
)

// schemaFS is the CUE schema source, exported by the SDK module (the contract
// repo) — files sit at the FS root, so consumers concatenate with dir ".".
var schemaFS = sdkschema.FS

// cueSchemaCtx is the process-wide CUE context (schemas compile once, reuse).
var cueSchemaCtx = cuecontext.New()

// sharedCueSchema is every schema/*.cue file unified into one value (no package
// clauses → one shared scope, so kind defs reference the shared #Step/#Context).
// The concatenation is the SINGLE contract shared with the dev-time generator
// (schemaconcat.ConcatSchema — R3), so the compiled schema can never drift from the
// generated Go types.
var sharedCueSchema = func() cue.Value {
	body, _, err := schemaconcat.ConcatSchema(schemaFS, ".", nil)
	if err != nil {
		panic(fmt.Sprintf("read embedded schema: %v", err))
	}
	v := cueSchemaCtx.CompileString(body)
	if v.Err() != nil {
		panic(fmt.Sprintf("CUE schema failed to compile: %v", errors.Details(v.Err(), nil)))
	}
	return v
}()

// cueKindDefs maps a kind name to its entity definition path (e.g. "#Candy").
var cueKindDefs = map[string]string{}

// registerCueKind records that `kind` is validated by the CUE def at defPath.
// Panics on a duplicate name or a def absent from the compiled schema —
// fail-fast at process start (mirrors mustCalVer).
func registerCueKind(kind, defPath string) {
	if _, dup := cueKindDefs[kind]; dup {
		panic(fmt.Sprintf("duplicate CUE kind registration: %q", kind))
	}
	if d := sharedCueSchema.LookupPath(cue.ParsePath(defPath)); d.Err() != nil {
		panic(fmt.Sprintf("CUE kind %q: definition %s not found: %v", kind, defPath, d.Err()))
	}
	cueKindDefs[kind] = defPath
}

// cueKindDef returns the compiled entity definition for a kind.
func cueKindDef(kind string) (cue.Value, bool) {
	dp, ok := cueKindDefs[kind]
	if !ok {
		return cue.Value{}, false
	}
	return sharedCueSchema.LookupPath(cue.ParsePath(dp)), true
}

// coreCueSchema packages the process-wide compiled CUE schema handle every relocated
// CUE-validate seam call passes through (K1 unit 2) — built fresh from the still-core D-data
// (cueSchemaCtx / sharedCueSchema / cueKindDef, all unchanged above) so call sites never
// reconstruct the struct by hand.
func coreCueSchema() spec.CueSchema {
	return spec.CueSchema{Ctx: cueSchemaCtx, Root: sharedCueSchema, KindDef: cueKindDef}
}

// validateEntityClosedCUE is now sdk/loaderkit.ValidateEntityClosedCUE (K1 unit 2); this file keeps
// a same-named/same-signature core wrapper (R3, mirrors cueDocFromYAML/validateNodeDocCUE/
// applyCueDefaults below) since validate.go and several corpus/tighten tests call it by that name.
//
// validateEntityClosedCUE unifies a single entity with #<Kind> and validates it WITHOUT requiring
// concreteness — it catches closedness violations (unknown keys) and type/enum/regex conflicts,
// but not missing-required fields. This is the LOAD-time check (restores the deleted unmarshalers'
// typo-detection), AND (since c9befd83) the sole remaining `charly box validate` entity-schema
// gate: its former sibling validateEntityCUE (concrete-required) was a dead-code-radical-removal-
// batch deletion — every kind this project's schemas currently model has no meaningfully-required
// field concreteness would catch beyond what closedness already does (verified against
// #Box/#Builder: every field is optional or carries a default), and the modern load-time
// plugin-kind gate (RDD-verified live: `plugin kind:<X>: plugin_input fails #<X>Input`) is the
// actual production entity-schema enforcement path today, superseding the legacy per-kind Go-side
// validateVocabularyCollections/validateEntityCUE pair (also deleted) for every kind beyond box.
func validateEntityClosedCUE(kind, label string, entity cue.Value) error {
	return requireProjectLoader().ValidateEntityClosedCUE(coreCueSchema(), kind, label, entity)
}

// assembleAndValidateEntitySteps/validateEntityNodeRec are now sdk/loaderkit's own internal
// assembleAndValidateEntitySteps/ValidateEntityNodeRec (K1 unit 3c, completing the K1 unit 2
// deferral) — pure recursion + CUE validate, zero registry coupling, so nothing outside loaderkit
// calls them and no core wrapper is needed.

// validateCandyManifestCUE is now sdk/loaderkit.ValidateCandyManifestCUE (K1 unit 3c); this file
// keeps a same-named/same-signature core wrapper (R3) since validate.go calls it by this name. The
// host supplies the registry-derived Threaded snapshot + the resolved DocParser (loaderkit never
// queries the registry itself).
func validateCandyManifestCUE(path string, data []byte) error {
	return requireProjectLoader().ValidateCandyManifestCUE(path, data, loaderThreaded(), requireLoaderParser(), coreCueSchema())
}

// validateNodeFormSteps is now sdk/loaderkit.ValidateNodeFormSteps (K1 unit 3c); this file keeps a
// same-named/same-signature core wrapper (R3) since validate.go calls it by this name.
func validateNodeFormSteps(path string, data []byte) error {
	return requireProjectLoader().ValidateNodeFormSteps(path, data, loaderThreaded(), requireLoaderParser(), coreCueSchema())
}

// cueDocFromYAML ingests one YAML document into a cue.Value (the whole doc) via the relocated
// CUE-validate seam (sdk/loaderkit.CueDocFromYAML, K1 unit 2) — kept as a same-named/same-signature
// core wrapper (R3): provider_kind_invoke.go (the TRUE clause-M kind dispatch) calls it directly,
// and validate.go (K3 box-validate engine, deferred to W2 per the spike) calls it too. Its former
// sibling callers assembleAndValidateEntitySteps/validateEntityNodeRec fully relocated to
// loaderkit as unexported internals (K1 unit 3c) — they call sdk/loaderkit's own CueDocFromYAML
// directly and no longer route through this core wrapper.
func cueDocFromYAML(path string, data []byte) (cue.Value, error) {
	return requireProjectLoader().CueDocFromYAML(coreCueSchema(), path, data)
}
