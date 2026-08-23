---
title: Authoring a plugin
description: Every capability charly has is a plugin candy — including ones that live in no repository of ours.
sidebar:
  order: 2
---

> **A gourmet kitchen: a tiny core, an all-you-can-eat buffet.** The core stays small on purpose —
> a kernel that loads, composes, gates, builds, and dispatches, and nothing else. Every capability
> the kitchen has — every verb, mold, probe, builder, and command — is a candy speaking one shared
> SDK, so the buffet grows without the core growing.
>
> — [tenet: where the factory is heading](/vision/)

Core is a plugin host. Verbs, entity kinds, deploy targets, install steps, builders and CLI
commands are all contributed by **plugin candies**, and the dispatch layer cannot tell the
difference between one compiled into the binary and one loaded over gRPC.

## A plugin is a candy

The single thing that makes a candy a plugin is a `plugin:` block:

```yaml
my-plugin:
  candy:
    version: 2026.180.1200
    description: |-
      What this plugin provides.
    plugin:
      providers: [verb:myprobe]
      source: github.com/my-org/my-repo/candy/my-plugin
    plan:
      - check: the myprobe verb dispatches and passes
        myprobe: { marker: hello }
        context: [runtime]
```

`providers:` lists the reserved words this plugin serves, each as `<class>:<word>` — the classes
are `kind`, `verb`, `deploy`, `step`, `builder`, `command` and `build`. Every word in the
[provider index](/reference/providers/) got there from one of these declarations.

## Placement is a free choice

A provider runs either **compiled into** the `charly` binary (in-process) or **out-of-process**
over gRPC, and the same provider works either way with zero authoring change. Placement is
decided by one thing: whether the candy is listed in `charly/charly.yml`'s `compiled_plugins:`.

Each plugin's page here states which it is, computed from that list rather than described in
prose — so the statement cannot go stale.

Choosing is mostly about what belongs in a shipped binary. A dev-time tool should not be in one:
the generator that builds this very site (`candy/plugin-docs`, serving `command:docs`) is
deliberately left out of `compiled_plugins:`, because it is run on a contributor's machine to
regenerate the docs and nowhere else. charly prescans its declared word into the CLI grammar and
connects it lazily on the first `charly docs` invocation.

The one thing that makes that free is self-containment: a plugin that needs the host reverse
channel is constrained toward the compiled-in placement, while a plugin that only reads and
writes its own data — like the docs generator — runs anywhere.

## Out-of-tree plugins

A plugin does not have to live in this repository at all. Point `source:` at your own repo:

```yaml
plugin:
  providers: [verb:myprobe]
  source: github.com/my-org/my-repo/candy/my-plugin
```

charly fetches the repo, builds the provider binary on the host, and serves it out-of-process.
Your plugin is a standalone Go module importing only the SDK ([software development kit](/concepts/00-vocabulary/#the-abbreviations)) (`github.com/opencharly/sdk`) —
never charly core. That import boundary is what keeps a plugin buildable outside this tree.

Because it is out-of-tree, it will not appear in the [plugin reference](/reference/providers/) on
this site: that catalog is generated from the candies in the OpenCharly repositories. Document
yours the same way this one is documented — the `plugin.providers` declaration, a per-plugin
`schema/*.cue` for the parameter grammar, and a non-empty candy `description:`. Those three are
the plugin's public surface.

## The parameter schema

A verb's input grammar is declared once, in CUE, inside the plugin:

```
candy/my-plugin/schema/myprobe.cue
```

That single source generates the plugin's Go parameter types for development and answers the
`Describe` RPC at runtime. It is also what renders as the parameter reference on the plugin's
page here.

## See also

- **[The plugin internals recipe card](/recipes/internals/plugin/)** — the boundary law, the
  authoring recipes, and the full CUE-schema contract.
- **[Provider index](/reference/providers/)** — every reserved word and the plugin serving it.
