# candy/ — signpost (not the rule-set)

You are in the **candy** definitions (`candy/<name>/candy.yml` + supporting
config files: `pixi.toml`, `package.json`, `Cargo.toml`, service files, …).

**Load these skills FIRST (R0):**

- `/charly-image:layer` — the authoritative `candy.yml` schema: the `task:` verb
  catalog, `vars:` substitution, the unified `service:` schema, package
  sections, `check:` checks, and the mandatory `version:` field.
- `/charly-image:image` — when composing candies into a box.
- `/charly-check:check` — authoring the `check:` declarative checks a candy ships.

The `layer-validator` agent is a fast pre-edit sanity gate; `charly box validate`
is the authoritative checker. Use the `charly candy …` editor verbs (comment-
preserving) rather than hand-editing where possible.

**Authoritative rules live in the repo-root `CLAUDE.md`** (one level up). R0–R10,
the hard-cutover policy, and AI attribution are defined there — this file only
signposts and restates no rule. History lives in this repo's `CHANGELOG/` (one file per CalVer version).
