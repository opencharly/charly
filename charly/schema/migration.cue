// migration.cue — the SCHEMA for the declarative migration table (the DATA lives
// in charly/migrations.cue). These defs are validation-only: the `charly migrate`
// engine unifies each table entry against #Migration at process start (fail-fast,
// like registerCueKind). They are NOT charly param structs, so each is @go(-) (no
// gengotypes type) AND migration.cue is excluded from the param-gen concatenation
// (schemagen excludeParamGen), exactly like node.cue's disjunction wrappers.
//
// CUE validates the SHAPE of each op; the "exactly one of ops/apply" rule and the
// CalVer ordering are enforced in Go (charly/migrate_engine.go) with actionable
// errors — CUE's field-presence comparison is too fragile for that gate.

// One migration step: a CalVer, a label, and EITHER a list of declarative ops OR
// a named Go escape-hatch hook.
#Migration: {
	version:       #CanonCalVer // CalVer this step lands files at; engine runs version > file-version, ascending
	name:          string       // short label for --dry-run / progress
	touches_host?: bool | *false // also rewrite the per-host overlay?
	ops?: [...#MigrationOp]      // declarative ops, applied in order (mutually exclusive with apply)
	apply?: string              // named Go hook for a structural reshape (mutually exclusive with ops)
} @go(-)

// Where an op applies: only the top-level mapping (root) or recursively (any).
#Scope: "root" | "any" @go(-)

// The declarative op vocabulary — one closed arm per op, discriminated by `op:`.
#MigrationOp: #RenameKey | #DeleteKey | #RemapScalar | #MoveKey @go(-)

// Rename a mapping key, preserving its value + comments.
#RenameKey: close({op: "rename_key", from: string, to: string, scope: #Scope | *"any", under_kind?: string}) @go(-)

// Delete a mapping key/value pair.
#DeleteKey: close({op: "delete_key", key: string, scope: #Scope | *"any", under_kind?: string}) @go(-)

// Remap a scalar value under a key (e.g. target: host -> target: local).
#RemapScalar: close({op: "remap_scalar", key: string, from: string, to: string, under_kind?: string}) @go(-)

// Relocate a key/value pair from one child mapping to another (simple reparent).
#MoveKey: close({op: "move_key", key: string, from_parent: string, to_parent: string, under_kind?: string}) @go(-)
