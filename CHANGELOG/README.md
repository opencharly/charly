# Changelog

**This `CHANGELOG/` directory is this repository's home for historical content.**
Every repository in the project keeps its **own** `CHANGELOG/` — history is
repo-scoped, never centralized in one file, and split into one file per CalVer
release version so no single file grows without bound.

`CLAUDE.md`, `README.md`, `plugins/README.md`, and every skill
(`plugins/**/SKILL.md`) describe the **current** state of the system — present
tense, forward-looking. Any reference to a previous version, a past rename, a
completed cutover or migration, a relocated / deleted / retired identifier, a
"previously / formerly / was / no longer", a dated change note, or a
commit-referenced cautionary tale belongs **here** and nowhere else. This
directory is the sanctioned "changelog context" named by CLAUDE.md R5's grep
self-test.

## Layout

- **One file per CalVer release version:** `<YYYY.DDD.HHMM>.md` (e.g.
  `2026.183.1359.md`). The CalVer is computed once per landing and shared by the
  changelog filename and the release git tag (`v<YYYY.DDD.HHMM>`), so each file
  maps to exactly one release. Entries use the project's `YYYY-MM-DD` date stamp
  inside the file.

## Index

- [2026.183.1359](2026.183.1359.md) — OpenCharly genesis (fresh history on `github.com/opencharly`)
