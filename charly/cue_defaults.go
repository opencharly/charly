package main

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
//
// The mechanism itself is sdk/loaderkit.ApplyCueDefaults (K1 unit 2); this file keeps a
// same-named/same-signature core wrapper (R3) for its one production call site
// (host_build_config_resolve.go).
func applyCueDefaults(kind string, out any) error {
	return requireProjectLoader().ApplyCueDefaults(coreCueSchema(), kind, out)
}
