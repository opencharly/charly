---
title: The box is the boundary
description: Safety lives at the walls of the running candybox — never in a shrunken toolset.
sidebar:
  order: 1
---

> **Secure the room, not the candy.** Safety lives at the boundary of a candybox — rootless
> containers, isolated VMs, encrypted volumes — never in a shrunken toolset. A walled room you
> can hand over *completely* beats an empty sandbox you keep narrowing.
>
> — [tenet 1](/vision/)

## The idea

The usual way to give an agent a working environment is subtractive. You start from a shell and
take things away: fewer commands, no network, no package installs, no root. Safety is bought by
removing capability, and most of the usefulness leaves with it. Worse, the subtraction never
finishes — every incident adds another entry to the deny-list, and the list is a running guess
about which capabilities are dangerous.

Candyboxing inverts the direction. The boundary is a **candybox** — a running, isolated thing
with kernel-enforced walls: a rootless container, a VM, or a check bed. Inside it, people and
agents get the *entire* toolset: a package manager, a compiler, nested containers, a browser,
root if the box grants it.

The practical rule that follows is the one worth remembering: **never secure by whitelisting
commands.** Trust the walls, not the tools. A whitelist has to predict which command is
dangerous. A wall does not have to predict anything.

Note the word: the boundary belongs to the **candybox**, not the box. The box is the image sitting
in storage — an inert artifact with no boundary at all. The candybox is what you get when that
image is *running* somewhere isolated. See [the words](/concepts/00-vocabulary/) if that
distinction is new.

## In practice

The [`tutorial-shell`](/reference/box/fedora/tutorial-shell/) box is an ordinary box, and this is
what handing one over looks like:

```bash
charly --repo opencharly/distro-fedora box build tutorial-shell
charly --repo opencharly/distro-fedora shell tutorial-shell
```

You are now inside the candybox. There is no command filter between you and the system — `dnf`
works, so does `rg`, so does anything else you install. The container runs rootless as uid 1000
with no `--privileged` and no added capabilities; the walls are the container's, not a policy
layer's.

Scale that up and nothing about the model changes, only the contents. The
[`fedora-coder`](/recipes/coder/fedora-coder/) box carries roughly thirty candies — five AI coding
CLIs, language runtimes, DevOps tooling — and still runs at uid 1000 with the same absence of a
command allowlist. It also runs *nested* rootless containers and rootless libvirt VMs inside
itself, without additive capabilities.

## See also

- **[The words](/concepts/00-vocabulary/)** — box vs candybox, defined.
- **[Disposable-flag semantics](/recipes/internals/disposable/)** — the authorization rules in full.
- **[Boxes all the way down](/concepts/12-boxes-all-the-way-down/)** — the boundary applied to charly itself.
