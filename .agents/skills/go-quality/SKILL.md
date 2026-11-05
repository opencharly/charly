---
name: go-quality
description: |-
  Check charly's Go source for AGENTS.md compliance + code quality: golangci-lint v2, gopls, go vet, gofmt, the .go compliance checklist (R2/R3/R5), the legitimate-pattern allowlist. Use before a Go code-quality audit or lint sweep.
---

# go-quality — checking & fixing charly Go source for project-rulebook compliance (`AGENTS.md`)

The `charly/` CLI is one large `package main` (~570 files, ~169K LOC). This skill is the
operational HOW for auditing it against the checkable subset of the project rulebook and driving the
fixes. It owns the *tooling + method*; the rule definitions live in `/charly-internals:strict-policy`
(R1–R5), the source map in `/charly-internals:go`, and landing in `/charly-internals:cutover-policy`
+ `/charly-internals:git-workflow`.

## Toolchain (all declared in the `dev-tools` candy)

| Tool | Role | Invocation (from `charly/`, where `go.mod` lives) |
|---|---|---|
| `golangci-lint` v2 | the deterministic backbone — bundles staticcheck/unused/errcheck/ineffassign/dupl/gocritic/misspell/revive/gocyclo/… | `golangci-lint run ./...` |
| `gopls` | references / call-sites / rename — confirm a symbol is truly unused before deleting | `gopls references`, `gopls call_hierarchy` |
| `go vet` | compiler-adjacent correctness | `go vet ./...` |
| `gofmt` | formatting drift | `gofmt -l .` |
| `go test ./...` | unit smoke (NOT the R10 gate) | `go test ./...` |

`golangci-lint` is **v2** — the config schema is `version: "2"` (not v1). `dupl` is a BUNDLED
linter, NOT a standalone tool (it is absent from the AUR — do not try to install it). There is
no `deadcode`/`staticcheck`/`misspell` standalone install either; all are bundled. No
`go install` of any linter (that would be an R3 workaround).

## The config — `charly/.golangci.yml`

Lives next to `charly/go.mod` (the module root), NOT the repo root. It enables the standard
set + the broadest curated quality/idiom/duplication linters, uncapped (`max-issues-per-linter:
0`) so an audit sees EVERY issue. **`gosec` is deliberately excluded**: on a subprocess-
orchestrating CLI its findings are dominated by the legitimate `exec.Command` pattern (the exact
false-positive class below); vulnerability scanning is `govulncheck`'s separate job.

## The pre-PR FORMATTER gate + the docstring/comment R5 sweep (both cost real review rounds)

Before opening ANY Go PR, run BOTH of these — `golangci-lint run` alone catches NEITHER,
and each has cost a CHANGES-REQUESTED review round in a real landing:

1. **The formatter gate**: `gofmt -l .` (must print nothing) **AND**
   `golangci-lint fmt --diff` (must print nothing) — the second catches
   formatter-class drift (import grouping, gofumpt-style spacing) that plain
   `golangci-lint run` does NOT report and `gofmt` does not fix.
2. **The docstring/comment sweep (R5, claim-keyed)**: a refactor that MOVES or
   DELETES a function must sweep its textual references, not just its call sites —
   grep for (a) ORPHANED DOCSTRINGS: a doc-comment still describing a function that
   moved elsewhere, and (b) STALE COMMENTS naming deleted/renamed identifiers or
   aliases. The grep self-test is word-boundary (`\b<id>\b`) and every hit is READ —
   a comment hit is as blocking as a code hit. The converse holds too: a comment
   mention is NOT a live caller — verify a "caller" grep hit is a call site before
   treating a symbol as used (a doc-comment reference once masqueraded as a caller
   and nearly kept dead code alive).

## Running the audit

```bash
cd charly
golangci-lint run ./...                 # full broadest run (uncapped)
golangci-lint run ./... | grep -E '^\* '   # per-linter summary counts
gofmt -l .                              # files needing format
go vet ./...
```

Run it uncapped to get the current baseline, and read the per-linter SHAPE rather than the
total — which linter leads tells you what kind of debt you are looking at. Neither a count
nor a distribution is pasted here: both would be stale before they were read, and a stale
SHAPE misleads more quietly than a stale total.

**Measurement gotcha — measure with the configured run, never `--enable-only`.** The
authoritative count is `golangci-lint run ./...` (the configured set), which DEDUPLICATES
across linters: a function flagged `unused` is NOT also reported by `unparam` for its params,
and a complex function shows ONCE (e.g. as `gocyclo`). `golangci-lint run --enable-only unparam`
DISABLES that dedup and surfaces "phantom" hits the configured run attributes elsewhere — so a
category can read 3 under `--enable-only` and 0 in the real gate. Always trust the configured
run; it is what CI and contributors see. (Corollary under "Fixing": a `//nolint` can shift which
linter "wins" the dedup — see there.)

## Auto-fix safety — never blanket `--fix`

**`golangci-lint run --fix` corrupts this source tree** (confirmed with v2.12.2):
gocritic's autofixer rewrites multi-statement blocks into a one-liner whose body it emits as a
literal `{ ... }` elision placeholder — the `...` is gocritic's snip marker, written verbatim
into the file → `syntax error: unexpected ...` (broke `ssh_client.go` + `deploy_executor_nested.go`,
`go build` failed). Recovery: `git checkout -- charly/` then `go build ./...`.

- **`gofmt -w .` is the ONLY safe blanket auto-fix** (pure formatting, AST-identical).
- Fix every linter category **manually / per-finding with review**, or `--fix` scoped to a
  SINGLE proven-safe linter at a time — NEVER gocritic. golangci-lint is for *finding*, not
  auto-*fixing*. (Memory: [[golangci-lint-fix-corrupts-source]].)

## Mechanical type-repoint sweeps — the collision catalog

Repointing a symbol across many files (dropping a package-main alias and qualifying every consumer with its real package — `Symbol` → `pkg.Symbol`) is a distinct hazard class from the lint-finding fixes above: a plain word-boundary text/regex substitution cannot see Go's AST, so it can silently rewrite something that is NOT a bare type reference. Seven collision classes recur often enough to check for before any such rewrite, roughly in order of how often they appear:

1. **A method sharing the type's name.** `func (r Receiver) Symbol(...)` is a distinct AST node from the type `Symbol` — a text rewriter matches both identically. Grep `func.*)\s*Symbol\(` first; a hit means the METHOD NAME stays bare, only its signature's type positions (params/return) get qualified.
2. **Composite-literal FIELD KEYS.** `Struct{Symbol: value}` — Go field names are never package-qualified. Exclude any occurrence of the symbol followed by `:` (modulo whitespace) inside a `{...}` literal; the VALUE side (`Symbol: &Symbol{...}`) still needs its own bare `Symbol` (the type reference) qualified.
3. **Local-variable / parameter shadowing.** If the bare symbol name, or the target PACKAGE's name reused as a local variable (e.g. a local named `spec` shadowing the `spec` package), is ALSO a local identifier in the same function, qualifying a sibling reference inside that scope makes every other bare access in the function ambiguous. Grep `:=\s*Symbol\b` and parameter lists using the target package's own name as a variable before rewriting; rename the shadowing local (not the package reference) when found.
4. **Variadic `...Type` ellipsis vs. the selector-dot exclusion.** A rewrite that excludes "preceded by `.`" (to skip `x.Field` selectors) must NOT also exclude Go's `...Type` ellipsis (three dots, not a selector) — otherwise a real bare-type variadic parameter (`func f(xs ...Symbol)`) is silently skipped and ships un-rewritten.
5. **Tool self-exclusion path-compare mismatches.** A rewrite script that excludes "already hand-fixed" files from a batch pass must compare paths in the SAME form both sides produce — `git grep -l` / `grep -rl` emit a `./`-prefixed path; comparing that against a bare filename tuple never matches, so the script silently re-includes a file it meant to skip.
6. **Field keys vs. bare types, amplified for maximally-generic names.** A short, generic symbol name (a verb/noun any codebase reuses widely — the worst case is a 2-letter type used as a dispatch selector, often an Op/verb-style discriminator) tends to independently collide as a FIELD NAME on multiple UNRELATED structs at once (e.g. five different structs each declaring their own field literally named after the type). Class 2's exclusion rule still covers every instance, but the volume alone means the proactive scan must run against the WHOLE consumer set, not a sample — a symbol this generic can have hundreds of field-key occurrences alongside a handful of genuine type references.
7. **Field DECLARATIONS have no colon.** `type S struct { Symbol string }` is NOT caught by class 2's colon-exclusion (Go's field-declaration syntax has no colon at all) — search separately for the bare symbol as a field name in a struct body (`^\s*Symbol\s+\w`) before rewriting; the field NAME stays bare even when co-located with genuine type references in the same struct.

**The mitigation ladder, in order:**

1. **Proactive collision scan FIRST**, before any rewrite — grep for method declarations, field declarations, composite-literal keys, and local-variable shadowing of the target symbol across the WHOLE consumer set, not a sample.
2. **AST-aware rewrite over regex — but ONLY for selector fields and method names; `gofmt -r` is NOT immune to classes 2/3.** `gofmt -r 'Symbol -> pkg.Symbol'` operates on the parsed AST, so it genuinely cannot touch `x.Symbol` selector expressions or `func (r T) Symbol(...)` method declarations — those are different AST node kinds from a bare type reference. **Verified live (go1.26.5) against a real collision fixture, it is NOT safe for the other two identifier-shaped classes**: `gofmt -r` WILL wrongly rewrite composite-literal field keys (class 2 — `Holder{Symbol: v}` silently becomes the INVALID `Holder{pkg.Symbol: v}`) and shadowed local identifiers (class 3), because both are bare identifiers the rewrite pattern cannot distinguish from a genuine type reference at that AST position. So: prefer `gofmt -r` for a collision surface confined to classes 1/4/5/7, but for classes 2/3 — with EITHER tool, `gofmt -r` or hand edits — the proactive scan (step 1) is the PRIMARY defense, not a suggestion; every class-2/3 hit it finds needs a manual exclusion or fix regardless of which rewrite mechanism you otherwise use. Fall back to per-file hand edits — never blind regex — for anything neither `gofmt -r` nor the scan resolves; add the import separately (`gofmt -r` does not manage imports).
3. **The compiler is the final gate, never a formality — REGARDLESS OF REWRITE TOOL.** `go build` and `go vet` catch every remaining defect precisely — an invalid field name, an ambiguous identifier, an unused import — before a single test runs. This is NOT optional even after an AST-aware rewrite: `gofmt -r` itself can silently emit invalid code for classes 2/3 (step 2), so the compiler — not the choice of tool — is what actually proves a repoint safe. Never ship a repoint the compiler has not actually built against the FULL final diff; a green build/vet on the complete, final tree is the acceptance bar, not a smoke check.

**The per-commit PR-body convention** for a multi-symbol repoint: one row per commit — symbol(s) → file count → collision classes found (by number, from the catalog above) → gate result — so a reviewer can audit commit-by-commit without re-deriving the whole sweep.

## gopls `modernize` — the Go 1.26 idiom set (auto-fix IS safe, with a compile gate)

The modernize idioms (`interface{}`→`any`, `strings.Cut`, `slices.Contains`, `maps.Copy`,
`CutPrefix`/`CutSuffix`, range-over-int, `slices.Backward`, `WaitGroup.Go`, `slices.Sort`,
`errors.AsType`, `new(expr)`) are NOT in the golangci-lint backbone — they are gopls analyzers.
Run + fix tree-wide from `charly/`:

```bash
go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest ./...        # findings (STDERR, not stdout)
go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest -fix ./...    # apply (re-run; overlapping fixes need >1 pass)
```

Unlike gocritic's `--fix`, the OFFICIAL modernize `-fix` is SAFE — but still gate it on `go build`:
- Findings print to **STDERR** (redirect `2>file`, not `>file`).
- It applies in passes ("applied N of M; re-run to apply more") — re-run until 0.
- Disable a risky category with `-<name>=false` (e.g. RDD a category first).
- **`omitzero`** removes the no-op `,omitempty` from **JSON tags only** (struct-typed fields),
  preserving the yaml tag — behavior-neutral; it explicitly SKIPS the behavior-CHANGING
  `omitempty`→`omitzero` alternative. Safe to apply.
- **`newexpr`** rewrites `boolPtr(x)`/`intPtr(x)`/`ptrBool(x)` call sites to `new(x)` but does
  NOT touch test-file calls or delete the now-dead wrapper helpers → CASCADE: after `-fix`, the
  wrappers are `unused` (delete them) and any remaining test calls must be rewritten too;
  `uintPtr(0)`→`new(uint)` (zero-value form — modernize can't infer the type). Needs `go 1.26`
  in go.mod. The `errorsastype` fix can leave a dangling bound var (`if x, ok := …AsType[…]`
  where the body never uses `x`) → `go build` catches it; change to `_, ok`.

## Dead-code (`unused`) — legitimate-keep patterns (do NOT delete)

golangci-lint `unused` is reliable for `package main`, but some flagged symbols are
intentionally retained — deleting them is wrong. Flag (don't delete) a symbol that:
- carries an explicit **retention doc-comment** ("kept for …", "retained because …");
- is referenced by a closure that is itself assigned-but-not-called (`check := func(){…}; _ = check`)
  — deleting the symbol breaks `go build` even though `unused` flags it;
- is named only in **comments / cross-references** (incl. comments in OTHER files / tests) —
  deleting orphans the reference;
- is deliberately-unwired scaffolding for a not-yet-shipped path.
Always confirm with `go build ./...` + `go test ./...` after deletions: a deletion that breaks
either means the symbol WAS used (transitively) — restore + flag it. A stale retention comment
whose named consumer no longer exists is its own R1 incident (fix the comment, then re-evaluate).

**RCA each dead symbol — removable vs missing-caller bug.** An unused exported symbol the
`unused` linter CANNOT see (it skips exported) is found by grepping call sites repo-wide; an
unused UNEXPORTED one may be a *latent bug* (a caller accidentally dropped) rather than obsolete
code. To find WHEN/WHY a symbol lost its caller, use the pickaxe — but **`git log -S 'sym(' --
charly/` is a TRAP**: a directory rename (the historical `ov/`→`charly/` move) makes EVERY
symbol's token count go 0→N under the new path, so the rename masquerades as the add/remove
commit. Run `git log --all -S 'sym('` WITHOUT the pathspec to find the true caller-removal
commit, then read its message: a deliberate relocation ("behavior moved to X") → delete the
orphan; an accidental drop → it may be a bug to re-wire (escalate if the disposition is a design
call). Verdict A (genuinely dead) only after the behavior is confirmed relocated or never-built.

## The `.go` compliance checklist

**Linter-covered (deterministic — let golangci-lint find these, agents only triage + dedup):**
dead code/`unused`, `ineffassign`, `unconvert`, `staticcheck` correctness, `gocritic` idiom,
`misspell`, `gocyclo` complexity, `errcheck` unchecked errors, `errorlint` wrapping, `unparam`,
`prealloc`, and `dupl` cross-file/in-file duplication (R2).

**Agent-only (linters cannot express — grep + judgment):**
- **R3 workarounds**: `time.Sleep` poll/retry loops lacking a sync primitive; unnamed magic
  numbers/durations; retry-on-flake; environment-specific shims; ad-hoc
  `podman`/`docker`/`virsh`/`systemctl` that BYPASS charly (see allowlist for the legitimate ones).
- **R5 stale paths**: `deprecated`/`DEPRECATED`/`TODO` markers with un-swept old code paths;
  references to deleted identifiers (grep the identifier — only `CHANGELOG/` should remain).
- **Repo invariants**: YAML-tag ↔ Go-identifier plural/singular symmetry; mode purity
  (`LoadConfig` must NOT read the deploy overlay / `charly.yml`); InstallPlan IR sync points;
  the "charly CLI is the only operational interface" rule.

## Legitimate-pattern allowlist — do NOT flag these (the #1 noise source)

- `exec.Command("podman"|"virsh"|"systemctl"|"pacman"…)` where **charly IS the orchestrator**
  — these are the operational interface, not ad-hoc bypasses. Forbidden is an ad-hoc such call
  against a charly-managed resource OUTSIDE the orchestration path.
- Idiomatic `errcheck` ignores: `defer x.Close()`, `fmt.Fprint*` to stderr/stdout, `os.Setenv`
  in tests — the fix is an explicit `_ = …` (makes intent visible), not handling.
- Named, justified `time.Sleep` polls in device/container/mount liveness (adb, gocryptfs) where
  no event/sync primitive is exposed by the underlying tool.
- Standard mode bits (`0o755`/`0o644`/`0o600`), well-known ports/IDs documented in domain code.

## Triage + adversarial verification (mandatory before any fix)

False positives auto-"fixed" become regressions. Every candidate finding is **adversarially
verified** — ≥3 skeptics, each prompted to REFUTE with a distinct lens (coincidence-not-dup /
legitimate-orchestration / no-sync-primitive-exists / idiomatic-ignore / would-break-surrounding-
idiom), **defaulting to refuted when uncertain**. Only majority-confirmed findings are fixed.

## Fixing — a handful of large thematic cutovers (NOT per-finding)

Group ALL confirmed findings into ~8 large cutovers, each fixing a whole homogeneous category
(tens-to-hundreds) as ONE atomic commit, R10-gated as a whole. Order low-risk/high-value →
higher-churn: dead-code → dupl(R2) → staticcheck → errcheck → idiom/quality → gocyclo →
agent-only rules. Cutovers land SEQUENTIALLY (one Go package — no concurrent mega-edits); the
EDITS within a cutover parallelize by file-cluster.

**`gocyclo` is a HEURISTIC — triage refactor-vs-nolint by SHAPE, never blanket-extract.**
Cyclomatic complexity scores a 60-case config `switch` identically to deeply-nested branching,
but the switch is trivially readable and extracting it just relocates the cases. So per function:
- **REFACTOR** a *sequential phase orchestrator / emitter* (a build pipeline, a Containerfile
  generator, an R10-sequence runner) — extract each cohesive phase into a named helper in the
  SAME file (behavior-preserving; byte-identical output for emitters). Genuinely clarifies.
- **`//nolint:gocyclo // <reason>`** a *flat cohesive* function — a config-key switch, a verb-set
  enumeration, a protocol decoder/state-machine, a sequential peer-validator, a diagnostic
  narrative — where artificial extraction HARMS readability. Each nolint carries a specific reason.
- When in doubt, nolint (zero-risk) over a risky extraction; a huge function (139) that can't
  reach the threshold by extraction alone gets the major phases extracted AND a residual nolint.

**`//nolint` shifts the cross-linter dedup — watch for unmasked findings (R1).** Adding
`//nolint:gocyclo` to a function that was ALSO (silently) an `unparam`/`unusedparams` hit removes
the gocyclo report, so the configured run now surfaces the previously-deduped finding. Resolve
the unmasked finding in the SAME cutover (e.g. remove an always-constant param, or `//nolint:
unparam` a uniform-handler signature) — don't nolint-stack blindly.

## R10 gate by change class (the commit gate)

`go test ./...` + `golangci-lint run` + `scripts/bootstrap-charly.sh` are smoke, not the gate — the gate is `charly check run <bed>` on the bed(s) that exercise the change, disposable-only, fresh-rebuild, zero warnings, pasted proof. The bed-selection matrix (incl. cross-cutting fan-out by owner) is owned by `/charly-check:check` "R10 gate by change class"; see also `/charly-internals:cutover-policy`.

**A mechanical pre-commit backstop is wired in every harness that roots here, and is
separate from the R10 gate.** The `pre-commit-gate.sh` hook (`.claude/hooks/`) runs the
CONFIGURED `golangci-lint run` on every module a commit's `*.go` change touches and blocks
a commit that is not lint-clean (fail-open when golangci-lint is absent). It is wired as a
`PreToolUse(Bash)` hook in `.claude/settings.json` (Claude Code, since umbrella `5e485a8`,
2026-08-28), in `.reasonix/settings.json`, and via `~/.kimi-code/config.toml`, which
delegates to `.claude/hooks/`. Where it runs, it does NOT replace the bed R10 gate (a green
linter proves compilation, not behaviour); it only guarantees the `golangci-lint` smoke is
green at commit time. Doctrine: `/charly-internals:agents` "Hooks doctrine".

**The hook's lint temp dirs live on the root fs.** The hook redirects its golangci-lint
run to a per-user cache (`~/.cache/charly-gate-lint/`) and creates the `TMPDIR`/`GOTMPDIR`
subdirs there before running golangci-lint, so the lint never writes into a capped system
temp dir. The constraint that motivates it — the per-command write cap, and the
"parallel golangci-lint is running" message that is really an ENOENT on a missing
`$TMPDIR` directory — is stated once in `/charly-internals:go` "Environment constraints —
sandboxed /tmp + Go temp dirs"; see there for the diagnosis and the fix.

## Cross-References

- `/charly-internals:strict-policy` — R1–R5, RDD, the forbidden-pattern catalogs (the WHAT).
- `/charly-internals:go` — the Go source map (file → responsibility).
- `/charly-internals:skills` — the companion **skill↔code source-map sync audit** (docs side).
- `/charly-internals:cutover-policy` + `/charly-internals:git-workflow` — landing the fixes.
- `/charly-check:check` — the R10 bed gate every code cutover passes.

## When to Use This Skill

Invoke before running golangci-lint / dupl / a duplication check, auditing charly Go source
for project-rulebook compliance or code quality, or planning/landing a compliance fix sweep.
