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

## The pin-currency control

This repo's `box/*` gitlinks are pins, and a pin that is never advanced is a pin that
drifts. The control that keeps them honest is `charly task pin-current`, run by
`.github/workflows/pin-currency.yml` on every PR that touches the pin surface and daily
on a schedule.

- **What it asks.** Whether every `box/*` gitlink is at the ref this repo's own
  `.gitmodules` declares for it — and it FAILS only on **neglect**: a pin that is not
  current *and* that this repo has not advanced for longer than its own bound (168 h).
  "Behind" alone is churn, not neglect: measured over this repo's own history, each of
  the six `box/*` twins is behind the ref it declares for most of the time — on 17 to 27
  of the 32 days from 2026-09-22 to 2026-10-23, and on 28 of those 32 (88 %) at least
  one twin was behind — so a gate on the distance would be red roughly three days out
  of four. That is the permanently-red gate that trains every reader to ignore it. The
  bound is on the **age of this repo's own last advance** of that path.
- **The bound is placed by measurement, not by taste.** Over this repo's own advance
  history — the gaps between the commits that recorded each twin's gitlink — the
  longest gap that stayed within a 7-day bound (ordinary operation) was 6.4 d
  (`box/cachyos`, 2026-07-09 → 2026-07-16), and 25 gaps ran past it, from 8.0 d to
  25.1 d: genuine lapses. 168 h (7 d) is the **only whole-day bound strictly between
  those two** — 0.6 d above the healthy maximum, so it does not fire on ordinary
  operation, and 1.0 d below the shortest lapse, so it catches the neglect. Over the
  9 days from 2026-10-15 (the day the leaves' intent landed) to 2026-10-23 the control
  was red on 6 of them, and every one of those was a day the twins had genuinely not
  been advanced — ages of 7.5 d to 14.7 d, every one inside a real lapse: 8.1 d
  (`box/cachyos`), 10.2 d (`box/arch`, `box/debian`, `box/fedora`, `box/ubuntu`) or
  15.3 d (`box/omarchy`). There was no red day on a healthy tree.
- **The bound is this repo's own number.** The producer's knob is opt-in and carries
  no default (unset ⇒ no bound at all), so 168 h is a choice this repo made on this
  repo's own evidence. Re-derive it when the cadence changes.
- **The bound is a parameter, not a constant in the rule.** `charly task pins` takes
  `MODE` (`status|bump|verify|currency` — `bump` advances EVERY pin this repo declares
  and does not commit) and `STALE_AFTER`; `pin-current` is the thin
  wrapper that sets `MODE=currency` and `STALE_AFTER=168h`. Change the bound by changing
  the wrapper, re-derive it with `scripts/measure-pin-cadence.py --bound-hours <h>`, and
  say in the PR why the new bound is right.
- **The only sanctioned advance is `charly task sync`** (the `pins` task with
  `MODE=bump`). Never hand-pin a gitlink, never check out a branch to "catch up", never
  write the SHA into the index by hand. The verb advances EVERY pin this repo declares
  from the remote and records the pin — it rolls the checkouts that exist and stages
  their gitlinks, and it does not commit; a hand-pin cannot be judged and is therefore a
  failure.
- **A pin that is not current and REPORTED is not a failure.** The control's message says
  which pins are not current and which are neglected; only a neglected pin turns it red.
- **The control proves it can fail.** `charly task pin-currency-proof` runs the control
  over a scratch fixture with the wiring **extracted from this repo's own
  `charly.yml`** — a pin not current and un-advanced since 2020 must go red, and the
  same pin advanced recently must pass. It runs SIX arms, in three pairs over three
  twin shapes: a `behind` twin, a **squash-shaped** one whose served ref does not
  descend from the recorded pin (the `not-on-the-served-line` class the org's own
  squash merges produce), and one that is **not materialized** (the `differs` class —
  not current because there is no checkout to judge it, which is the shape of a light
  tree). Within each pair the arms differ only in the date of the commit that
  recorded the pin, so the verdict flips with the age and nothing else. A gate that
  cannot fail is not a gate; run it before you trust the control.
- **The measurement's classifier is proved too.** `scripts/measure-pin-cadence.py
  --self-test` builds a scratch twin with the shapes the control has to tell apart
  and asserts the class each one earns — one arm per class the classifier can
  return: `cur`, `behind`, `not-on-the-served-line`, `unmerged`, and
  `unjudgeable` (a pair whose commits the twin's object store does not carry, so
  the ancestry question is undecidable) — and which of them the control treats as
  neglect-eligible. The proxy this script used to carry — *the pin is not the tip,
  so it is behind* — calls a squash-advanced pin `behind`, because the two commits
  differ; the self-test is what says the difference is real, and it is what makes
  the class tally in the measurement readable rather than assumed.

*Detail:* the umbrella `AGENTS.md` Part II #3 (pins are gitlinks; producer-first; a pin's
state is `cur`/`behind`/`blocked-by`/`not-on-the-served-line`).

## The pin-advance claim control

The `sync` task's own description used to call the advance a non-mutating kind — it
said the advance changed nothing, and that it did not commit — and the FIRST half was
FALSE: the verb advances every pin this repo declares, rolls the checkouts that exist
and stages their gitlinks. An operator who read that line would have run a wave they
believed was a report. The control that keeps the claim true lives in this repo,
because this repo declares the pins and owns the words about them — the umbrella's
own gate for the same rule says, in its own header, that it speaks for the umbrella's
prose only.

- **What it asks.** `scripts/check-pin-verb-scope.py` reads this repo's own tracked
  tree over three arms: **SCOPE** (every claim-maker surface states that the advance
  touches EVERY pin this repo declares — the CLAIM, not the keyword, because "every"
  alone also matches a question about currency), **NO RETIRED CLAIM** (no tracked file
  calls the advance a non-mutating kind) and **COVERAGE** (every file that names the
  verb is classified, so a new claim-maker surface cannot slip in unclassified). The
  surfaces are located by structural anchors, so a surface that moves is a finding,
  not a silent pass.
- **It is run two ways.** `charly task pin-verb-scope` runs the control and
  `charly task pin-verb-scope-proof` runs its own proof; both are steps of the
  `check-task` R10 bed, and `.github/workflows/pin-verb-scope.yml` runs the control
  in CI.
- **The tracked tree is the INDEX, and its exemptions are ONE rule.** The tree is
  `git ls-files -s -z`: the index decides WHICH files are in scope, and each tracked
  path's content is read from disk, so the working tree decides WHAT they say. The
  index modes that are exempt from it live in ONE committed file,
  `scripts/tracked-files.txt`, which BOTH this control and the retired-knob surface
  control (`charly/retired_build_selector_test.go`) read — so the exemptions have ONE
  home and the two gates cannot drift on WHICH modes are exempt. Each reader parses
  that file in its own language, so a copy of the LIST in a reader would be a second
  list, and two lists drift (R2). That file's own header specifies its grammar; read
  it there, never copy it here. Edit that file, never a reader's copy.
- **The control proves it can fail.** `python3 -I scripts/check-pin-verb-scope.py
  --self-test` turns each of the arms RED on its own mutation — a surface that drops
  the scope, a surface whose line is gone, a file that calls the advance a
  non-mutating kind (including the exact wording this defect carried), a file that
  names the verb without being classified, and a classification that has gone stale —
  while the shipped tree stays GREEN in the same run. A gate that cannot fail is not
  a gate.

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
