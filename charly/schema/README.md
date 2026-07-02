# charly/schema — the single-source CUE schema

Every `charly.yml` shape (box / candy / bundle / vm / k8s / deploy / …) is defined
ONCE here, in package-less `*.cue` files. These files are the single source of
truth for two consumers:

1. **Runtime validation (ingress).** `charly/cue_schema.go` `sharedCueSchema`
   `//go:embed`s `schema/*.cue`, concatenates them (sorted, newline-joined) into
   ONE compiled `cue.Value`, and validates every loaded entity against its
   `#<Kind>` def. See `/charly-build:validate`.
2. **Generated Go param types + vocabulary (`charly/spec`).** `task cue:gen`
   turns these same files into the committed `charly/spec/*_gen.go` — so the Go
   structs the loader decodes into, and the kind/verb/method word lists the CLI
   dispatches on, can never drift from what the schema validates.

> The package-less files MUST stay package-less — `sharedCueSchema` concatenates
> them into one scope. The `package spec` clause + the file-level `@go(spec)`
> attribute that `cue exp gengotypes` needs are injected by the gen pipeline
> (`charly/internal/schemagen -mode=concat`), NEVER written into the source.

## Regenerating `charly/spec` — `task cue:gen`

```sh
task cue:gen
```

The task (`taskfiles/Cue.yml`):

1. **Bootstraps the pinned cue CLI** into `./bin/cue` (gitignored) — `v0.16.1`,
   the SAME version as charly's embedded `cuelang.org/go` library, so the CLI that
   runs `gengotypes` matches the library that compiles the schema. It prefers a
   pre-fetched scratch binary, else downloads the checksum-pinned release tarball.
2. **Concatenates** `schema/*.cue` via `charly/internal/schemagen -mode=concat`
   (the SAME order as the runtime `sharedCueSchema` — one concatenation contract),
   headed `package spec` + `@go(spec)`.
3. **`cue exp gengotypes`** → `charly/spec/cue_types_gen.go` (the Go param structs).
4. **`charly/internal/schemagen -mode=vocab`** → `charly/spec/vocab_gen.go`
   (`KindWords`, `DocDirectives`, `StepKeywords`, `ContextWords`, `OpFields`, … —
   all read straight from the compiled schema via the cue API).
5. **`gofmt`** both committed generated files.

Both runs are **reproducible**: two consecutive `task cue:gen` invocations produce
no diff, and `TestGenReproducible` (in `charly/spec`) fails CI if the committed
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
  generated type) and hand-written in `charly/spec/union_types.go` (the SAME
  package, so the generated structs reference them by name). The suppressed defs:
  `#Step`, `#Matcher`, `#MatcherList`, `#ContainsList`, `#PackageItem`, `#StrMap`,
  `#VmSource`, `#VmSSH`, `#CandyApk`, `#LibvirtHostdev`, `#LibvirtListen`,
  `#Tunnel`, `#PortScope`, `#Ephemeral`, `#Preemptible`. A scalar-containing
  disjunction is suppressed with a trailing value attribute — `(…) @go(-)` — so
  `{} & string` can never collapse the validation; an all-struct disjunction takes
  a bare trailing `@go(-)`.

## Egress schemas (moved out — M16)

The egress schemas (the CUE charly validates the config it WRITES against) NO LONGER live here.
They moved to the compiled-in `candy/plugin-egress/egress-schemas/` (+ `vendor/cloud_config.cue`) when
egress was externalized into a plugin — `charly/egress.go` is now a thin shim. See
`/charly-internals:egress`. So `schema/*.cue` here is purely the INGRESS schema; only the `node.cue`
node-disjunction grammar defs appear as (harmless) generated types in `cue_types_gen.go` (excluded
from param-gen by `excludeParamGen`).
