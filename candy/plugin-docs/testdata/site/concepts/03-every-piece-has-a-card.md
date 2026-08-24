---
title: Every piece has a card
description: Every candy, box and verb carries a dedicated page — generated from the source, never retyped.
sidebar:
  order: 3
---

> **Every candy ships with its recipe card.** Every candy, box, and verb carries a dedicated
> skill, so nothing in the candy store is a mystery — neither you nor your agents ever have to
> guess what a piece does, how it's made, or how it should taste.
>
> — [tenet 3](/vision/)

## The idea

A catalog is only useful if it is complete and current. Both properties are hard to sustain by
hand: a hand-written catalog omits whatever was added last, and describes whatever was true when
someone last looked.

So the catalog on this site is not written — it is **projected**. `charly docs generate` reads the
sources that already exist (each candy's own `charly.yml`, each plugin's CUE schema, the skill
corpus) and emits the reference and recipe trees wholesale on every run. A deleted candy
disappears rather than lingering as an orphan page. Regeneration on a clean tree is a no-op, and
drift is treated as a defect rather than a chore.

Two consequences worth knowing. First, the catalog enumerates what is **defined**, not what
happens to be enabled — a plugin that is not compiled into the binary still has a page, because it
still loads when a plan references it. Second, a candy's own `description:` field *is* its
documentation; there is no second place to write it, and no way for the two to disagree.

The same reasoning applies to the piece you are reading. The narrative pages — the quickstart,
these concepts, the guides — are hand-written, because an argument cannot be projected from a
config file. Everything factual is.

## In practice

Every piece has a page, and you can reach it three ways.

**Browse it.** The box this site teaches with has a card of its own:
[`tutorial-shell`](/recipes/distros/tutorial-shell/), alongside its generated
[box reference](/reference/box/fedora/tutorial-shell/) listing what it composes. Each candy it
composes has one too — [`ripgrep`](/recipes/tools/ripgrep/), [`sshd`](/recipes/coder/sshd/),
[`supervisord`](/recipes/infrastructure/supervisord/).

**Look it up by word.** If you meet a reserved word and want to know what implements it, the
[provider index](/reference/providers/) maps every one to its owning plugin.

**Ask the image.** The card is not the only copy — the acceptance plan travels *inside* the built
image as an OCI label — a key/value pair carried inside the image itself, per the [Open Container Initiative](/concepts/00-vocabulary/#the-abbreviations) format:

```bash
charly --repo opencharly/distro-fedora box inspect tutorial-shell
```

That is what lets a pulled image be self-describing and self-testable without its source
repository, which the next pages build on.

## See also

- **[Recipe cards](/recipes/)** — the full catalog.
- **[The cookbook never lies](/concepts/10-the-cookbook-never-lies/)** — why generated beats written.
- **[The charly CLI](/guides/the-cli/)** — how the command surface maps to plugins.
