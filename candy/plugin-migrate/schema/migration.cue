// migration.cue — the SCHEMA for the declarative migration table (the DATA lives
// beside it in candy/plugin-migrate/migrations.cue). This file lives IN the plugin,
// NOT sdk/schema: #Migration is validation-only, consumed ONLY by this plugin's
// engine and by no charly core code — so per the kernel/plugin boundary law
// (/charly-internals:plugin) a plugin-only schema lives in its plugin, never the SDK
// contract. The engine embeds it (//go:embed schema/migration.cue) and unifies each
// table entry against #Migration at process start (fail-fast, like registerCueKind).
//
// #Migration.version pins to #CanonCalVer, which STAYS the SDK's single source of
// truth (sdk/schema/version.cue) — the engine concatenates THIS file with the SDK's
// version.cue to compile #Migration standalone, without duplicating #CanonCalVer and
// without pulling charly's full ingress schema. The defs are @go(-) (no gengotypes
// type); the "exactly one of ops/apply" rule and the CalVer ordering are enforced in
// Go (engine.go) — CUE's field-presence comparison is too fragile for that gate.

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
