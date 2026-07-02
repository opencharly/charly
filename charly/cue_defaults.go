package main

import (
	"fmt"

	cueyaml "cuelang.org/go/encoding/yaml"
	"gopkg.in/yaml.v3"
)

// applyCueDefaults fills schema-declared defaults into an already-RESOLVED
// entity by unifying its marshaled form with #<Kind> and decoding back. It is
// the unify-AFTER-merge counterpart to the loader's decode (which deliberately
// does NOT unify, so merge/inheritance see unset-as-zero): run this only at the
// point an entity is finalized for use, never at load.
//
// Only REQUIRED-with-default schema fields materialize — an optional-with-
// default field (`field?: *x`) stays absent on unify and does not reach the
// struct, so a value the caller never set for such a field is unaffected. A
// field already carrying a value is preserved (unify keeps the concrete value;
// the default only fills the gap). The canonical use is `firmware: *"bios"` in
// schema/vm.cue, which is required-with-default precisely so it materializes.
//
// Because it round-trips through the CLOSED #<Kind> schema, the entity must
// already validate against it (it does — the loader validated it). The
// round-trip is lossless for every modeled field; see cue_defaults_test.go.
func applyCueDefaults(kind string, out any) error {
	def, ok := cueKindDef(kind)
	if !ok {
		return fmt.Errorf("applyCueDefaults: no CUE schema registered for kind %q", kind)
	}
	b, err := yaml.Marshal(out)
	if err != nil {
		return fmt.Errorf("applyCueDefaults %s: marshal: %w", kind, err)
	}
	af, err := cueyaml.Extract("defaults", b)
	if err != nil {
		return fmt.Errorf("applyCueDefaults %s: cue ingest: %w", kind, err)
	}
	cv := cueSchemaCtx.BuildFile(af)
	if cv.Err() != nil {
		return fmt.Errorf("applyCueDefaults %s: cue build: %w", kind, cv.Err())
	}
	merged := cv.Unify(def)
	if merged.Err() != nil {
		return fmt.Errorf("applyCueDefaults %s: unify with #%s: %w", kind, kind, merged.Err())
	}
	if err := merged.Decode(out); err != nil {
		return fmt.Errorf("applyCueDefaults %s: decode: %w", kind, err)
	}
	return nil
}
