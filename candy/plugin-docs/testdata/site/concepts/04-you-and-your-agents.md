---
title: You and your agents
description: One command surface, reachable from a shell or over RPC ([remote procedure call](/concepts/00-vocabulary/#the-abbreviations)) — with no second-class channel.
sidebar:
  order: 4
---

> **Two tasters at one bench.** The same `charly` surface serves you at the keyboard and your
> agents driving the line, with no second-class channel for either. Built for you *and* your
> agents, in the same breath.
>
> — [tenet 4](/vision/)

## The idea

Tools that grow an agent story usually grow a second, smaller surface for it — a handful of
endpoints wrapping the parts someone judged safe to automate. That surface then lags: it is
maintained separately, it lacks the newest verbs, and the agent ends up shelling out anyway.

Here there is one surface. The `charly` binary is also an MCP server, and every leaf command is
exposed as an MCP tool by reflection over the same command model the CLI uses. Nothing is
hand-listed, so nothing can lag: a new verb is reachable over RPC the moment it exists.

That symmetry is what makes the rest of this site's claims hold for both readers. When a page says
"prove it on a disposable bed", the agent runs the identical command you would — same verbs, same
exit codes, same output. There is no automation dialect to learn and no capability that exists
only at the keyboard.

Authoring is part of the same surface, and it is the part that matters most for an agent. Editing
YAML by regenerating it destroys comments and key order; charly's editor verbs go through the YAML
*node* API instead, so a machine edit leaves a human-authored file intact.

## In practice

Expose the whole CLI over RPC. `mcp` is an out-of-process command plugin, discovered from a
project's `candy/plugin-mcp` rather than compiled in — so point charly at a project that provides
it. `--repo` does that without a checkout:

```bash
charly --repo opencharly/charly mcp serve               # Streamable HTTP or stdio
charly --repo opencharly/charly mcp serve --read-only   # filters the destructive tools out
```

The verb set is not fixed at build time. Run against a project without that candy and the verb
simply is not there.

Author a candy the way an agent would — the same verbs you would use by hand, with comments and
key order preserved across every edit:

```bash
charly box new project my-project
charly -C my-project box new candy my-tool
charly -C my-project candy add-rpm my-tool ripgrep
charly -C my-project candy set my-tool env.MY_VAR value
charly -C my-project box new box my-shell --base fedora --candy my-tool
charly -C my-project box validate
```

`-C` names the project every verb acts on, so nothing depends on which directory you are standing
in. `--repo` is the read-only counterpart: it resolves a *published* project into a cache, which is
right for reading and wrong for editing — a scaffold written to your project is invisible to it.

And when the agent wants to know whether its change worked, it runs what you would run:

```bash
charly --repo opencharly/distro-fedora check run check-tutorial-shell
```

## See also

- **[The charly CLI](/guides/the-cli/)** — the command surface and how plugins serve it.
- **[The MCP command](/recipes/build/charly-mcp-cmd/)** — the gateway in detail.
- **[The spec is the test](/concepts/06-the-spec-is-the-test/)** — what an agent grades against.
