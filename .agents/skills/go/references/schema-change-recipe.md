### How to change the charly.yml schema (CUE is the single source of truth)

CUE (`spec/schema/*.cue` — the sdk contract module) is the SOLE author-of-record
for the `charly.yml` ingress schema; the Go param structs in `spec/spec` and the
reserved-word vocabulary are GENERATED / DERIVED from it (see "CUE is the single
source of truth"). The recipe:

1. **Edit CUE only.** Add or change the field, kind, verb, or method enum in
   `spec/schema/*.cue`. A param-struct field is just a CUE field; a new KIND is a new
   `#Node` arm + a per-kind `#Def`; a new VERB is a field on `#Op` (+ a
   `#*Method` enum if it carries methods). Keep the `#Def` CLOSED (closed by
   DEFAULT — that is what catches a misspelled field); for two mutually-exclusive
   fields use a disjunction applied with `&` (`#Box: {…} & ({from?: _|_} |
   {base?: _|_})`), NEVER an embedded `matchN`, which silently disables
   closedness (the comments in `box.cue` / `candy.cue` / `vm.cue` document this).
2. **Annotate for Go with `@go()`.** A multi-word field → `@go(GoName)` (wire key
   preserved); a named scalar you want as a plain Go `string`/`map` →
   `@go(,type=string)` merged as `@go(GoName,type=string)`; a pointer / tri-state
   field → `@go(GoName,optional=nillable)` (→ `*T`) or `@go(GoName,type=*int|*bool)`;
   a disjunction field → `@go(GoName,type=YourUnionType)` and hand-write that
   union in `spec/spec/union_types.go`; a never-authored field → `@go(-)`.
   NOTE: def-level `@go(CharlyName)` is BROKEN in cue v0.16.1 (it dangles the
   referencing fields) — expose a charly type NAME via a Go alias in
   `spec/spec/charly_names.go` (`type BoxConfig = Box`) instead.
3. **Regenerate: `charly task cue-gen`** in the spec repo (the base schema's own
   `kind:task`). It runs `cue exp gengotypes` into `spec/spec/cue_types_gen.go`, the companion
   `spec/internal/schemagen` into `spec/spec/vocab_gen.go`, and the
   principled yaml-tag retag transform (both over the `spec/schemaconcat`
   concatenation). NEVER hand-edit the generated files (they carry the
   `Code generated … DO NOT EDIT` banner).
   `TestGenReproducible` (`spec/spec/gen_repro_test.go`) fails if committed ≠ fresh.
4. **Bind behavior.** A new GENERIC install-VERB (a kernel primitive — rare) adds
   ONE `VerbCatalog` handler in `charly/reserved_registry.go` binding the reserved
   word to its generated param type; the startup
   `checkVerbBijection(VerbCatalog, spec.OpVerbs, spec.AuthoringVerbs)` gate panics
   fast (and fails `TestReservedWordRegistry_*`) if CUE and the registry disagree.
   A new KIND is NOT a core edit — it is a PLUGIN (per the kernel/plugin boundary
   law): it serves its own schema over Describe and is gated by
   `checkKindProviderBijection(spec.KindWords)` (`charly/provider_kind.go`) against
   `spec.KindWords` (empty; every authoring kind is plugin-served). See
   `/charly-internals:plugin` "The kernel/plugin boundary law".
5. **The drift gates (keep them all green).** There is no `spec_parity_test.go`;
   field parity is enforced three ways. (a) The **compile-time alias surface** —
   most authored param types are used fully-qualified as `spec.X` directly (no
   charly-core alias at all); a shrinking residual subset still routes through
   charly's OWN dissolving `charly/*_aliases.go` files (K4/K5 migration
   inventory — see "The alias surface" above; do not rely on this skill for
   which symbols currently route through which file, grep the actual file
   instead) — either way, a hand-referenced field that no longer has a
   matching spec field (name + wire-key + type) FAILS the build at that surface.
   (b) `TestGenReproducible` proves the generated files match a fresh `charly task cue-gen`. (c) The reserved-word bijection gate proves the kind/verb/method
   wiring matches CUE. New kind also needs its `spec/schema/<kind>.cue` `#<Kind>` def
   (reusing the shared defs in `_common.cue`) + a one-line `cue_kind_<kind>.go`
   `registerCueKind` registration + a corpus-test entry
   (`cue_kinds_corpus_test.go`).
6. **A schema change ships its migration-table entry.** Only if the change alters an authored
   WIRE key (the YAML users write) is it a FORMAT change: then it is CROSS-REPO — land + tag
   the spec repo, then in the superproject bump the `github.com/opencharly/spec` require
   version (charly/go.mod + every lockstep go.mod — the canonical-go.mod gate asserts one
   shared pin; `charly task mods-tidy` re-syncs the go.sum files) and append the matching
   entry to the declarative migration table (`candy/plugin-migrate/migrations.cue` — the
   TABLE lives in the compiled-in `command:migrate` plugin) per `/charly-build:migrate`.
   There is no `#SchemaVersion` / `version:` to bump: the table is an ordered, version-free
   list, and each entry is idempotent. A pure Go-identifier change via `@go()` is NOT a
   format change (wire key preserved) — no entry is needed.
7. **Guards (all must pass):** `cd charly && go test ./...` (reproducibility +
   bijection + corpus + closedness + embedded-defaults via
   `TestEmbeddedDefaults_SchemaConformance`) +
   `charly box validate` on the repo and every `box/<distro>` submodule + the R10
   bed gate.

Do NOT reintroduce a hand-maintained vocab list, a per-verb dispatch switch, or
a hand-written param struct — they are generated / derived from CUE now. Do NOT
use `cue get go` (that is the Go→CUE direction; CUE is the source here). The
ingress validation recipe is owned by `/charly-build:validate`; the egress
analog is the "Adding a new egress schema" recipe in `/charly-internals:egress`.
