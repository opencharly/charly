<!--
OpenCharly PR template. main advances ONLY through this PR + a green
charly/claude-validation status posted by the fresh pr-validator agent, which
merges it. Fill EVERY section — the validator FAILS an empty/template-only body.
See CLAUDE.md "Post-Execution Policies" + /charly-internals:git-workflow.
Do NOT compute the release CalVer here: use a placeholder CHANGELOG/<CalVer>.md;
the pr-validator finalizes the merge-time version at merge.
-->

## Summary of changes

<!-- What changed and why. One paragraph + a bulleted list of the concrete edits. -->

## How tested (R10)

**Change class:** <!-- docs-only | code/config | hook/workflow — per /charly-check:check "R10 gate by change class" -->

<!--
Paste the ACTUAL evidence for that class:
- code/config: the fresh-rebuild `charly check run <bed>` / `charly check live` output.
- hook/workflow: the live parse+execute output of the changed script.
- docs-only: the R5 grep self-test + cross-reference/markdown review result.
A --dry-run, a bare `go test`, or "will test later" is NOT the runtime gate.
-->

```
<paste evidence here>
```

## Attribution tier

<!-- One of: fully tested and validated | analysed on a live system | documentation reviewed.
     Must be justified by the evidence above (never inflated). `documentation reviewed`
     is legal ONLY when the whole diff is documentation. The commit carries the matching
     `Assisted-by: Claude (<tier>)` trailer. -->

## Rule-compliance checklist

- [ ] R1 — every failure/warning root-caused (no "flake"/"transient")
- [ ] R2 — every surfaced issue fixed here or its own immediate-next cutover (no "out of scope")
- [ ] R3 — no duplication; one shared abstraction
- [ ] R4 — no ad-hoc workaround (sync primitive, not sleep/retry)
- [ ] R5 — deprecated path + ALL stale refs deleted; `git grep` clean (only CHANGELOG context)
- [ ] R7/R10 — exercised end-to-end on a `disposable: true` target at its change-class gate (code/config)
- [ ] R9 — deployed binary matches source; runtime deps in package management (if applicable)
- [ ] Transitional/dual-mode/legacy code deleted BEFORE the acceptance run (FINAL code only)
- [ ] Relevant skills honored (name them)
- [ ] `CHANGELOG/<CalVer>.md` entry staged (placeholder CalVer — the pr-validator finalizes it)

---

*Assisted-by: Claude (&lt;tier&gt;)*
