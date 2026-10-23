---
name: plugin
description: |-
  Authoring/modifying a charly PLUGIN — a candy with a `plugin:` block contributing Providers (verbs/kinds/deploy-targets/steps/builders/commands), its CUE schema, builtin or external. Covers the unified Provider model, per-plugin CUE-schema contract, SDK, loader.
---

# Plugins — the unified Provider + per-plugin CUE-schema model

A **plugin is a candy** (`candy/<name>/charly.yml`) carrying a `plugin:` block. The single addition that
makes a candy a plugin is that block:

```yaml
my-plugin:
  candy:
    description: |-                 # mandatory (ADE)
      What this plugin provides.
    plugin:
      providers: [verb:myprobe]     # "<class>:<word>" — class ∈ kind|verb|deploy|step|builder|command|build
      source: github.com/org/repo/candy/my-plugin   # EXTERNAL (out-of-tree) is the DEFAULT for new plugins;
                                                  # `source: builtin` is the LEGACY in-repo linkage form
      primary:
        myprobe: marker             # scalar sugar target: `myprobe: hello` == {marker: hello}
    plan:
      - check: the myprobe verb dispatches and passes   # ADE: ≥1 deterministic check
        myprobe: { marker: hello }  # the verb sugar — map value = the plugin input verbatim
        context: [runtime]
```

A candy with no `plugin:` block is an ordinary candy; one WITH it is a plugin. Full candy authoring surface
applies (`/charly-image:layer`), including the mandatory `description:` and `plan:`+`check:` (ADE).

## Placement — EXTERNAL by default for new plugins

A plugin is authored placement-agnostic (the same provider serves both placements, F8) and the DEFAULT
for a NEW plugin is **external** (out-of-tree, out-of-process): projects compose it via the
`@github.com/<org>/<repo>/candy/<name>:<ref>` candy ref, and charly connects it by word at runtime
(host-builds `./cmd/serve`, speaks go-plugin gRPC) with ZERO charly-module changes — clean
core/sdk/plugin separation per the boundary law below. Compiling a plugin INTO the charly binary
(`compiled_plugins:` + a go.mod require + pluginsgen) is a per-charly-build DECISION reserved for
curated kernel-adjacent builtins; the strategic direction migrates those external over time, so do
not reach for it for a new plugin. Never write the placement into the description prose (it drifts;
the docs compute it from `compiled_plugins:`).

## A plugin's declarations are a PUBLISHED surface

`charly docs generate` (see `/charly-build:docs`) renders one **opencharly.ai** page per plugin
candy from three things this file already requires: the `plugin.providers` list, the per-plugin
`schema/*.cue`, and the candy `description:`. Two consequences:

- An empty, stale or placeholder `description:` is a public-docs defect, not just an ADE gate
  miss — it is the prose a reader gets for that plugin.
- **Placement is computed, never transcribed:** the page states compiled-in vs runtime by reading
  `charly/charly.yml`'s `compiled_plugins:`, so moving a plugin between placements updates its
  documentation automatically. Never write the placement into the description prose; it will
  drift the moment the list changes.

The same three declarations are how an OUT-OF-TREE plugin documents itself — the generator cannot
reach a repo it does not have, so its author publishes from the identical surface.

## Reference Index

| Topic | File |
|---|---|
| The kernel/plugin boundary law (E/M/B/D/R, the decision procedure, the incomplete-seam self-test incl. the per-kind-map tell + the generated-index precedent, the host-boundary-object trap, the resolve-to-envelope canonical shape) — sole owner of this doctrine — plus the v2 target architecture end-state | `references/boundary-law.md` |
| The unified Provider model (transport-invisible dispatch, the `build` class, lifecycle phases, flat vs structural kind decode), placement (builtin/external × in-proc/out-of-process at build and deploy time), and the four authoring recipes (external, compiled-in, host-coupled kit, external command) | `references/authoring-recipes.md` |
| The per-plugin CUE schema contract (dev-time Go params, runtime schema-over-Describe RPC), the load gate + validator, and why plugin schemas are self-contained | `references/cue-schema-contract.md` |

## Verification

- `go test ./...` — the registry/transport/schema seams (`TestPluginGRPCRoundTrip`,
  `TestExternalPluginEndToEnd` proves the schema travels over RPC, `TestPluginSchemaSpliceValidation`,
  `TestBuiltinPluginSchemasSplice` is the CI gate that every builtin schema splices).
- `charly task cue-gen` — regenerates the spec module's own types in the spec repo; each plugin's `params` package is regenerated in its OWN repo by the same pipeline. Both are reproducible (a second run is a no-op).
- `charly box validate` — the candy + `plugin:` block (`candy/plugin-box/validate_rules.go`'s
  `IsPlugin` check — an explicit, documented 1:1 port of the former core `validatePluginCandy`, deleted
  as dead code in the dead-code-radical-removal batch once its call site moved here —
  verifies each capability is well-formed and, for the LEGACY `source: builtin` linkage form, that
  the provider is actually compiled in; an external `source: github.com/...` candy needs no
  compiled-in wiring at all).
- R10: a disposable bed composing a builtin AND an external plugin, driven to a fresh `charly update` — the
  builtin's baked check + the external's out-of-process check both pass.

## Cross-References

- `/charly-internals:go` — the provider registry + the reserved-word spine (verbs/kinds/deploy/steps/builders/commands).
- `/charly-image:layer` — the candy authoring surface the `plugin:` block extends.
- `/charly-check:check` — the plugin-verb check steps (`<word>: <input>` sugar) + ADE (a plugin's own acceptance plan).
- `/charly-build:validate` — `charly box validate` rules.
- `/charly-internals:install-plan` — the `pluginDeployTarget` deploy lifecycle (`OpExecute` reverse channel, ledger record) + the `OpEmit` build-time fragment; the deploy wire types CUE-sourced at `spec/schema/deploy.cue` / `spec/schema/buildwire.cue` / `spec/schema/seam.cue`.
- `/charly-build:generate` + `/charly-internals:generate-source` — the build-time plugin connect seam + the `EmitTasks` placement-agnostic plugin-verb dispatch (`OpEmit` → fragment).

## When to Use This Skill

Invoke before authoring or editing any `plugin:` block, any `sdk/**` code, the plugin SDK, a
compiled-in plugin candy (`compiled_plugins:`) or host-coupled kit candy, an external plugin module, or the
plugin schema/param gen pipeline.
