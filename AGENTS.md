# OpenCharly charly core — agent rules

**The org-wide rulebook comes first and applies here in full.** It is the umbrella
`AGENTS.md` in [opencharly/opencharly](https://github.com/opencharly/opencharly/blob/main/AGENTS.md):
R0 Skills first and its Skill Dispatcher, Candyboxing, RDD, ADE, SDD, R1–R10,
Disposable-Only Autonomy, Hard Cutover, the Acceptance checklist, landing, and AI
attribution. Read it before acting. This file adds only the rules specific to the charly
core — the Go kernel / plugin host in this repository — and does not restate the umbrella's.
History belongs only in `CHANGELOG/`.

## R0 for the core

Load these skills, in addition to every row the umbrella dispatcher selects, before
touching the matching code:

| Trigger | Skill(s) to load |
|---|---|
| Go source work (adding/modifying `charly` commands) | `/charly-internals:go` |
| Go code quality / `golangci-lint` / `dupl` / `.golangci.yml` | `/charly-internals:go-quality`, `/charly-internals:strict-policy` |
| `spec/schema/*.cue` / `charly task cue-gen` / `cue_types_gen.go` / SDD | `/charly-internals:go`, `/charly-internals:plugin` |
| Plugin authoring / the plugin SDK / `compiled_plugins:` / host seams | `/charly-internals:plugin` |
| IR / InstallPlan / EmitTarget / OCITarget | `/charly-internals:install-plan` |
| `charly box build` / `charly box generate` / Containerfile | `/charly-build:build`, `/charly-build:generate`, `/charly-internals:generate-source` |
| `charly box validate` / schema error | `/charly-build:validate` |
| `charly migrate` / CalVer schema version | `/charly-build:migrate` |
| Egress config validation | `/charly-internals:egress` |
| OCI labels / capabilities contract | `/charly-internals:capabilities` |
| VmSpec / libvirt / cloud-init / OVMF internals | `/charly-internals:vm-spec` |

## The kernel/plugin boundary law

Core is a generic plugin host. It owns only plugin loading, prescan/dispatch (including the
per-node kind-decode registry resolve + provider invoke a plugin's Materializer seam calls
back into — the fold/not-found POLICY itself is a plugin), provider transport, and the
reverse-channel broker. Concrete kinds, schemas, validation, resolution, build, deploy, and
check behavior belong in plugin candies or SDK kits. A concrete-kind need creates a plugin;
a cross-plugin need creates a generic host seam.

Core imports only permitted contract surfaces, gains no kind-word switches or per-kind
maps, and never adds or grows alias files. *Detail (placement, transport, concurrency,
generated artifacts, egress):* `/charly-internals:plugin` — mandatory when dispatched.

## SDD in the core

The CUE schema (`spec/schema/*.cue`) is the single source for authored and wire types. Go
types are generated with `charly task cue-gen` (`cue exp gengotypes` →
`cue_types_gen.go`); never hand-transcribe a schema-shaped wire struct. Clean regeneration
is a no-op; any generation exception requires RCA and a live schema spike. *Detail:*
`/charly-internals:go`.

## R9 and the core Go gate

- Build the CalVer-stamped, worktree-local binary with `scripts/bootstrap-charly.sh`, invoke
  it through that worktree's `bin/`, confirm `bin/charly version` and dependency/gitlink
  consistency, and never install it as a shared binary.
- The core gate is `go test ./...` and `go vet ./...` from `charly/`, then the R9 build
  above. Never run bare module-wide Go commands from the superproject.
- Runtime OS dependencies belong in the charly candy's `packaging:` section
  (`packaging/charly.yml`). Repository maintenance is the `kind: task` surface in
  `charly.yml`, run via `charly task <name>`.

*Detail:* `/charly-internals:go`.

## Core key rules

- One `charly.yml` generic kind-container uses lowercase hyphenated names and shape-based
  routing; top-level names are unique within a document.
- `candy:` is the sole image/layer kind; `base:` or `from:` makes an image.
- Runtime plugins stamp only usable committed source provenance; failures remain errors at
  their source.
- Strict operator commands and idempotent internal reconciliation are separate contracts.

## Where things are documented

- The umbrella `AGENTS.md` — the org-wide rulebook (above); the umbrella's `VISION.md` —
  thesis and direction.
- `PROGRAM/` — binding program north-star documents (one file per program, named in every
  spawn brief).
- `README.md` and current subsystem docs — present behavior and user guidance.
- The opencharly/marketplace repo — every skill and the full skill index.
- [opencharly.ai](https://opencharly.ai) — generated from these sources by
  `charly docs generate` at the charly commit the docs repo pins; never hand-edit a
  generated page.
- `CHANGELOG/` — history only.
