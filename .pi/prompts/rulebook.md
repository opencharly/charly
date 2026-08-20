---
description: Quick reference: R0-R10, ADE, RDD, SDD, attribution tiers
---
## Charly Rulebook Quick Reference

### Engineering Discipline (R0-R5)
- **R0 - Skills First**: Load skills via `charly_load_skills` before first tool call
- **R1 - RCA**: Every anomaly gets root-cause analysis before remediation
- **R2 - Finish cutover**: No "pre-existing", "out of scope", "follow-up PR"
- **R3 - No duplication**: One canonical implementation per behavior
- **R4 - No workarounds**: No sleeps, retries, manual infrastructure commands
- **R4a - Fix product first**: Fix code before prose
- **R5 - Delete legacy**: Remove old path + every reference in one commit

### Artifact Verification (R6-R9)
- **R6 - Git safety**: Check status first, no force-push, no direct-main push
- **R7 - Prove behavior**: Run the changed path live, retain output
- **R8 - Preserve artifacts**: Validate labels, configs, schemas
- **R9 - Binary equals source**: `task build:binary`, invoke via `bin/`

### Acceptance Gate (R10)
- **R10**: Fresh rebuild on `disposable: true` target, pasted output, zero warnings

### Pillars
- **RDD**: Prove high-risk assumptions on a disposable bed before editing
- **ADE**: Every candy has a `plan:` with at least one `check:` step
- **SDD**: CUE schema before code, `task cue:gen` for generated Go

### PR Body Requirements
- ## Summary
- ## How tested (pasted commands + output)
- ## Project-rulebook rule-compliance (table)
- ## Change Classification
- *Assisted-by: ...* footer

### Attribution Tiers
- `fully tested and validated`: Full R10 gate passed
- `analysed on a live system`: Changepath ran live, R10 incomplete
- `documentation reviewed`: Docs-only
- `syntax check only`: Do not commit
- `theoretical suggestion`: Never ship