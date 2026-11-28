# OpenCharly charly core — agent rules

**The org-wide rulebook comes first and applies here in full.** It is the umbrella
`AGENTS.md` in [opencharly/opencharly](https://github.com/opencharly/opencharly/blob/main/AGENTS.md):
R0 Skills first and its Skill Dispatcher, Candyboxing, RDD, ADE, SDD, R1–R12,
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

## DESIGN.md is the contract's subject

The umbrella rulebook says how to work; `DESIGN.md` says what must be true when the work is
done — the contract's requirements, its invariants, and the data shapes every repo shares.
Read it before designing anything here: before adding a field, a shape or a mechanism, grep
the contract for it and read the row that governs the area. **Conformance to the rulebook
with a violation of `DESIGN.md` is not a pass.** When the two disagree, the disagreement is
a defect in one of them: fix the one that is wrong, in the open, and say so.
`spec/schema/*.cue` is the machine-readable projection of `DESIGN.md`, and `DESIGN.cue` is
its schema; where the prose and the CUE differ, the CUE is what the tooling enforces and the
difference is a defect to resolve — never a licence to pick whichever is convenient.

For this core the consequence is concrete: `charly` is a generic plugin host, so **core
never grows a per-kind branch that `DESIGN.md` places in a plugin.** The boundary law below
states the placement in principle; `DESIGN.md` is where each kind's placement is written
down, and a branch added here that the contract assigns to a plugin is a contract violation
whatever the boundary law's shorthand suggests.

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

## The pin-currency control

This repo's `box/*` gitlinks are pins, and a pin that is never advanced drifts. The
control that keeps them honest is `charly task pin-current`, run by
`.github/workflows/pin-currency.yml` on every PR that touches the pin surface.

- **What it asks.** Whether every `box/*` gitlink is at the **tip of the branch** its
  own `.gitmodules` entry declares — the branch each path's intent names — and it
  FAILS on a pin that is behind. There is no neglect bound and no age: the old 168 h
  bound and its cadence measurement are deleted, so a pin behind its branch fails
  whether it has been behind for a minute or a month. The strict failure replaces the
  argued one.
- **It consults no tag.** Currency is a branch question: the served tip of the
  declared branch, never a release tag. A tag marks a merge; it says nothing about the
  branch tip a consumer's gitlink should record.
- **The only sanctioned advance is `charly task ff`** (the `pins` task with
  `MODE=ff`). It fast-forwards every pin this repo declares to the tip the remote
  serves for that branch — it rolls the checkouts that exist and stages their
  gitlinks, and it does not commit. Never hand-pin a gitlink, never check out a branch
  to "catch up", never write the SHA into the index by hand; a hand-pin cannot be
  judged and is therefore a failure.
- **A pin that is behind and REPORTED is a failure.** The control's message IS the
  answer: it names the pins that are not at their branch's tip, and a behind pin turns
  the control red. `charly task ff` is the remedy.
- **The verb's MODE is a closed set, and only one mode writes.** `charly task pins`
  takes `MODE` (`status|ff|verify|currency`): `ff` fast-forwards EVERY pin this repo
  declares and does not commit, while `status`, `verify` and `currency` only read.
  `pin-current` is the thin wrapper that selects `MODE=currency`, and `ff` selects
  `MODE=ff`; both exist so the mode choice is made once.
- **The proof that it can fail lives with the producer.** The old six-arm bound proof,
  and the scratch-fixture script it ran, is deleted with the bound it proved. What
  proves the strict verdict now is the `git-submodules` verb's own tests in
  `opencharly/plugin-task`, which owns the verb: they prove `MODE=ff`
  fast-forwards and that `currency` FAILS on a behind pin. The cross-repo cutover is
  tracked as opencharly/opencharly#1347.

*Detail:* the umbrella `AGENTS.md` Part II #3 (pins are gitlinks; producer-first; a pin's
state is `cur`/`behind`/`blocked-by`/`not-on-the-served-line`).

## The pin-advance claim control

The advance half's own description used to call the advance a non-mutating kind — it
said the advance changed nothing, and that it did not commit — and the FIRST half was
FALSE: the verb fast-forwards every pin this repo declares, rolls the checkouts that
exist and stages their gitlinks. An operator who read that line would have run a wave
they believed was a report. The control that keeps the claim true lives in this repo,
because this repo declares the pins and owns the words about them — the umbrella's
own gate for the same rule says, in its own header, that it speaks for the umbrella's
prose only.

- **What it asks.** `scripts/check-pin-verb-scope.py` reads this repo's own tracked
  tree over these arms: **SCOPE** (every claim-maker surface states that the verb
  fast-forwards EVERY pin this repo declares — the CLAIM, not the keyword, because
  "every" alone also matches a question about currency), **NO RETIRED CLAIM** (no
  tracked file calls the advance a non-mutating kind), **COVERAGE** (every file that
  names the verb is classified, so a new claim-maker surface cannot slip in
  unclassified) and **ONE HOME** (the shared rule's GRAMMAR and its exempt-mode LIST
  live in the rule file's OWN header, and every reader that file names still reads
  it). The surfaces are located by structural anchors, so a surface that moves is a
  finding, not a silent pass.
- **It is run two ways.** `charly task pin-verb-scope` runs the control and
  `charly task pin-verb-scope-proof` runs its own proof; both are steps of the
  `check-task` R10 bed, and `.github/workflows/pin-verb-scope.yml` runs the control
  in CI.
- **The tracked tree is the INDEX, and its exemptions are ONE rule.** The tree is
  `git ls-files -s -z`: the index decides WHICH files are in scope, and each tracked
  path's content is read from disk, so the working tree decides WHAT they say. The
  index modes that are exempt from it live in ONE committed file,
  `scripts/tracked-files.txt`, which this control and the retired-knob surface
  control (`charly/retired_build_selector_test.go`) read — so the exemptions have ONE
  home and no reader can drift on WHICH modes are exempt. Each reader parses
  that file in its own language, so a copy of the LIST in a reader would be a second
  list, and two lists drift (R2). That file's own header specifies its grammar, and
  the ONE HOME arm holds that: the grammar and the LIST live in that header and
  nowhere else, and every reader the file names still reads it — read the grammar
  there, never copy it here. Edit that file, never a reader's copy.
- **The control proves it can fail.** `python3 -I scripts/check-pin-verb-scope.py
  --self-test` turns each of the arms RED on its own mutation — a surface that drops
  the scope, a surface whose line is gone, a file that calls the advance a
  non-mutating kind (including the exact wording this defect carried), a file that
  names the verb without being classified, a classification that has gone stale, a
  reader that restates the shared rule's grammar, a copy of that grammar inside the
  rule file's own other paragraph, a rule file whose header stops stating the
  grammar, a reader that copies the exempt-mode list, and a named reader whose code
  stops naming the rule file — while the shipped tree stays GREEN in the same run. A
  gate that cannot fail is not a gate.

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
