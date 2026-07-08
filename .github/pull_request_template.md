<!--
OpenCharly PR template. `main` advances ONLY through this PR + a green
charly/claude-validation status posted by the fresh pr-validator agent, which
merges it. FILL EVERY SECTION with EVIDENCE, not promises — the pr-validator
FAILS a body that leaves any applicable section blank, answers a rule with a
bare checkbox instead of HOW it was satisfied, or pastes no R10 output. Mark a
line `N/A — <reason>` where the change class genuinely excludes it (docs-only
skips the runtime rules). See CLAUDE.md "Ground Truth Rules" + "Post-Execution
Policies" + /charly-internals:git-workflow + /charly-check:check "R10 gate by
change class". Do NOT compute the release CalVer: use a placeholder
CHANGELOG/<CalVer>.md; the pr-validator finalizes the merge-time version.
-->

## Summary of changes

<!-- What changed and WHY. One paragraph + a bulleted list of the concrete edits.
     Every file/behavior in the diff must be accounted for here (a description↔code
     mismatch is a security FAIL). -->

## Change class

<!-- Pick ONE (drives which R10 gate + which rules apply below):
     docs-only | candy/box-config | charly-or-sdk Go | hook/workflow | cross-repo
     For cross-repo: list the repos + the producer→consumer landing order. -->

## How R10-tested — exact gate, commands, and pasted output

<!-- The validator re-runs this; give it the EXACT recipe + proof, not a summary. -->

- **The gate for this change class:** <!-- name it per /charly-check:check "R10 gate by change class" — e.g. `charly check run <bed>` for a touched code path; `charly box validate` + build + the composing bed for a candy; the concurrent /verify-beds roster for a cross-cutting loader/resolver/IR change; the non-runtime standards for docs-only. -->
- **Disposable target(s) used:** <!-- the `disposable: true` bed/deploy name(s) — or `N/A (docs-only)` with the reason. Testing on a non-disposable target is an R10 FAIL. -->
- **Fresh rebuild:** <!-- confirm R9: the binary was REBUILT from this source (`task build:charly`) and `charly version` matches; and the gate ran on a FRESH `charly update`/rebuild, at ZERO warnings. -->
- **Did the CHANGED code path execute live?** <!-- yes → the changed runner/branch ran, output below. If ONLY the success path ran (the new error/edge branch did not execute), say so — it caps the tier at `analysed on a live system`. A `--dry-run`, a bare `go test`, a rebuild WITHOUT running the changed piece, or "will test later" is NOT the gate. -->
- **Concurrency (shared-state changes only):** <!-- if this touches the loader/discover walk, deploy ledger, podman store, resource arbiter, VM/pod lifecycle, or a build lock: paste the CONCURRENT roster run (all beds at once, /verify-beds). "It passes on an idle/serial run" is NOT proof and is a FAIL. Every failure the roster surfaced is answered with its ROOT mechanism + fix, never "flake/environmental/load". Else `N/A`. -->

```
<paste the ACTUAL command(s) + output here — the fresh-rebuild gate output, and
for EACH changed code path the lines showing it executed. For docs-only: the R5
grep self-test result + the cross-reference/markdown review.>
```

## Attribution tier

<!-- One of: fully tested and validated | analysed on a live system | documentation reviewed.
     Justified by the evidence above, never inflated:
     - `fully tested and validated` requires the cutover's NEW/CHANGED code paths to have
       EXECUTED against the fresh rebuild (a changed branch that never ran live is at most
       `analysed on a live system`).
     - `documentation reviewed` is legal ONLY when the whole diff is documentation
       (`*.md` / comment-only / all-doc submodule bump).
     - `syntax check only` / `theoretical suggestion` must NOT ship.
     The commit carries the matching `Assisted-by: Claude (<tier>)` trailer. -->

## CLAUDE.md rule compliance — state HOW each is satisfied (or `N/A — <reason>`)

<!-- One line of EVIDENCE per rule (what you did / where to look), not a bare tick.
     These mirror the pr-validator's checklist; an unanswered applicable rule FAILS. -->

- **R0 skills:** <!-- which Skill-Dispatcher skills the change's area loads, and how it honors them -->
- **R1 RCA + zero warnings:** <!-- every failure/warning surfaced (build/test/lint/check/deploy) root-caused + fixed; gate output has ZERO warnings; no "flake/transient/environmental" -->
- **R2 no out-of-scope:** <!-- every issue surfaced during this cutover fixed here (blocking) or spun as its own immediate-next cutover — none parked as "pre-existing/follow-up" -->
- **R3 no duplication:** <!-- any repeated pattern unified into one shared abstraction; no `<name>-host`/`<name>-pod` siblings -->
- **R4 no workaround:** <!-- no sleep/retry/magic-number/env-shim; a race fixed with a sync primitive; no ad-hoc podman/docker/virsh/systemctl against charly resources -->
- **R5 hard cutover + grep:** <!-- paste/confirm `git grep '<removed-id>'` (and every false claim swept) returns only CHANGELOG context; NO transitional/dual-mode/legacy path in the FINAL code -->
- **R6 git safety:** <!-- any destructive git action was preceded by a status/stash check — or N/A -->
- **R7 runtime gate:** <!-- a runtime-affecting change ran the end-to-end bed gate, not just `go test` — or N/A -->
- **R8 artifact invariants:** <!-- a generation change asserted the emitted Containerfile sections + `ai.opencharly.*` labels post-build — or N/A -->
- **R9 binary == source + deps:** <!-- deployed binary rebuilt + `charly version` matches; new runtime OS deps in `pkg/arch/PKGBUILD depends=` — or N/A -->
- **R10 disposable + coverage:** <!-- proven on `disposable: true` only, fresh rebuild, zero warnings; ships the check/test coverage that would FAIL without this change -->
- **RDD / ADE / SDD:** <!-- RDD: high-risk assumptions (esp. composition-at-latest-versions) bed-proven, not doc-read. ADE: every new/changed candy has `description:` + `plan:` with ≥1 deterministic `check:` (`charly box validate` passes). SDD: a schema/`.cue` edit regenerated its `*_gen.go` and `task cue:gen` is a no-op — or N/A -->
- **Hard cutover:** <!-- ONE atomic commit per repo; no "Phase 2/TODO/deferred" left in scope; an approved plan executed as written -->
- **Kernel/plugin boundary law:** <!-- a core/`sdk` change is only a generic Envelope/Mechanism/Bootstrap-root/kind-Data — no concrete-kind schema/switch/per-kind-map leaked into the kernel; a new capability is a plugin — or N/A -->
- **Disposable-only autonomy:** <!-- any autonomous destroy/rebuild was on a `disposable: true` target — or N/A -->
- **Clean architecture + Go gates:** <!-- cleanest approach, deprecated code fully removed; for Go: `gofmt -l .` empty, `golangci-lint run ./...` = 0 issues, `go vet` clean, `go test ./...` green — or N/A -->
- **CHANGELOG:** <!-- `CHANGELOG/<CalVer>.md` staged (placeholder CalVer — the pr-validator finalizes it) -->

---

*Assisted-by: Claude (&lt;tier&gt;)*
