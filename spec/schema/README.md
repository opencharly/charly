# sdk/schema — the single-source CUE schema

Every `charly.yml` shape (box / candy / fleet / vm / kubernetes / deploy / …) is defined
ONCE here, in package-less `*.cue` files. These files are the single source of
truth for two consumers:

1. **Runtime validation (ingress).** The schema travels WITH this module as
   `schema.FS` (package `github.com/opencharly/sdk/schema`). charly core
   (`charly/cue_schema.go` `sharedCueSchema`) unifies every `schema/*.cue` file
   via `schemaconcat.ConcatSchema` (sorted, newline-joined) into ONE compiled
   `cue.Value`, and validates every loaded entity against its `#<Kind>` def.
   See `/charly-build:validate`.
2. **Generated Go param types + vocabulary (`sdk/spec`).** `task cue:gen`
   turns these same files into the committed `spec/*_gen.go` — so the Go
   structs the loader decodes into, and the kind/verb/method word lists the CLI
   dispatches on, can never drift from what the schema validates.

> The package-less files MUST stay package-less — `ConcatSchema` concatenates
> them into one scope. The `package spec` clause + the file-level `@go(spec)`
> attribute that `cue exp gengotypes` needs are injected by the gen pipeline
> (`internal/schemagen -mode=concat`), NEVER written into the source.

## Regenerating `spec` — `task cue:gen`

In THIS repository:

```sh
task cue:gen
```

The task (`Taskfile.yml`):

1. **Bootstraps the pinned cue CLI** into `./bin/cue` (gitignored) — `v0.16.1`,
   the SAME version as charly's embedded `cuelang.org/go` library, so the CLI that
   runs `gengotypes` matches the library that compiles the schema. It downloads
   the checksum-pinned release tarball (a no-op once `./bin/cue` is at the
   pinned version).
2. **Concatenates** `schema/*.cue` via `internal/schemagen -mode=concat`
   (the SAME `schemaconcat.ConcatSchema` contract as the runtime
   `sharedCueSchema` — one concatenation contract), headed `package spec` +
   `@go(spec)`.
3. **`cue exp gengotypes`** → `spec/cue_types_gen.go` (the Go param structs).
4. **`internal/schemagen -mode=vocab`** → `spec/vocab_gen.go`
   (`KindWords`, `DocDirectives`, `StepKeywords`, `ContextWords`, `OpFields`,
   `OpVerbs`, `AuthoringVerbs`, … — all read straight from the compiled schema
   via the cue API), and `-mode=version` → `spec/version_gen.go` (the
   schema-version consts from `schema/version.cue`).
5. **`gofmt`** the committed generated files.

The superproject's `task cue:gen` (`taskfiles/Cue.yml`) wraps this: it asserts
the two repos' cue pins match, runs THIS task first (base schema → `sdk/spec`),
then regenerates every plugin candy's `params` package from its own
self-contained `candy/plugin-*/schema/*.cue` via the SAME pipeline (R3).

Both runs are **reproducible**: two consecutive `task cue:gen` invocations produce
no diff, and `TestGenReproducible` (in `spec/`) fails CI if the committed
`*_gen.go` differs from a fresh regeneration. **Never hand-edit the `*_gen.go`
files** — change the `*.cue` source and regenerate.

## The `@go(...)` annotations

`cue exp gengotypes` is lossy: every CUE disjunction degrades to `any` /
`map[string]any` / an empty struct (Go cannot express a disjunction), and field
names get a naive `leadingCap + underscores-preserved` mapping. Two annotation
classes in the `*.cue` source fix this — both are **inert for runtime validation**
(CUE attributes never participate in unification):

- **Field renames** — `@go(<GoName>)` makes the generated Go field name match the
  hand-written charly param struct (`http` → `HTTP`, `cap_add` → `CapAdd`,
  `kernel-param` → `KernelParam`, …). The wire (JSON/YAML) key is preserved.
- **Union-type suppression + redirect** — the handful of disjunction defs whose
  faithful Go shape charly relies on are annotated `@go(-)` (suppressing the lossy
  generated type) and hand-written in `spec/union_types.go` (the SAME
  package, so the generated structs reference them by name). The current set is
  the source itself: grep `schema/*.cue` for `@go(-)` and read the matching
  hand-written types in `spec/union_types.go` — an enumerated copy here would
  drift. A scalar-containing
  disjunction is suppressed with a trailing value attribute — `(…) @go(-)` — so
  `{} & string` can never collapse the validation; an all-struct disjunction takes
  a bare trailing `@go(-)`.

## Egress schemas

The egress schemas (the CUE charly validates the config it WRITES against) do NOT
live here. They live in the superproject's compiled-in
`candy/plugin-egress/egress-schemas/` (+ `vendor/cloud_config.cue`), with
`candy/plugin-fleet/egress.go` as a thin shim — see `/charly-internals:egress`. So
`schema/*.cue` here is purely the INGRESS schema; only the `node.cue`
node-disjunction grammar defs appear as (harmless) generated types in
`cue_types_gen.go` (excluded from param-gen by `excludeParamGen`).
