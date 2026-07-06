# candy/ — signpost (not the rule-set)

You are in the **candy** definitions (`candy/<name>/charly.yml` + supporting
config files: `pixi.toml`, `package.json`, `Cargo.toml`, service files, …).

**Load these skills FIRST (R0):**

- `/charly-image:layer` — the authoritative candy schema: the compact node form
  (`<name>: {candy: <full body>}` with the ordered `plan:` step list), the
  step verb sugar, `env:`/`var:` maps, the unified `service:` schema, package
  sections, and the mandatory `version:` + `description:` + `plan:` fields.
- `/charly-image:image` — when composing candies into a box.
- `/charly-check:check` — authoring the `plan:` steps a candy ships (ADE).
- `/charly-internals:plugin` — when the candy carries a `plugin:` block.

The `layer-validator` agent is a fast pre-edit sanity gate; `charly box validate`
is the authoritative checker. Use the `charly candy …` editor verbs (comment-
preserving) rather than hand-editing where possible.

**Authoritative rules live in the repo-root `CLAUDE.md`** (one level up). R0–R10,
the hard-cutover policy, and AI attribution are defined there — this file only
signposts and restates no rule. History lives in this repo's `CHANGELOG/` (one file per CalVer version).
