---
title: Rebuild beats patch
description: When a fresh box costs what a patch costs, patching the wrong design stops being the pragmatic choice.
sidebar:
  order: 11
---

> **Free to forge a better candybox.** And when the box itself is wrong — the wrong mix of
> candies, a missing one, a composition that won't melt together — the agent forges a fresh box
> rather than make do with a broken design. Because a candybox is just a recipe, and a throwaway
> one, building the right box from scratch costs no more than patching the wrong one.
>
> — [tenet 11](/vision/)

## The idea

Most workaround culture is an economic response to a real constraint. When rebuilding an
environment costs an afternoon, patching around a flaw is genuinely the rational choice — and the
patches accumulate into the thing everyone is afraid to touch.

Change the cost and the calculus inverts. A box here is a declarative candy list, and a candybox
is disposable by construction, so building the *right* box costs roughly what patching the wrong
one costs. Once that is true, the workaround stops being pragmatic and starts being simply worse:
it carries the flaw forward, and it makes the recipe describe something other than what is
running.

The rule that follows: when the composition is wrong — a missing candy, the wrong mix, two that do
not compose — change the recipe and rebuild. Do not paper over it inside the running box, because
a fix applied to a running candybox is a fix that disappears on the next rebuild while the recipe
still describes the broken thing.

This is the same instinct as [reproducible, not merely successful](/concepts/08-reproducible-not-merely-successful/),
from the other end. That page is about the recipe continuing to produce the same box; this one is
about the recipe remaining the *only* description of it.

## In practice

Add the missing concern as a candy, compose it, rebuild — in a project of your own:

```bash
charly box new project my-project                              # a project to own
charly -C my-project box new candy my-tool                     # scaffold the candy
charly -C my-project box new box my-shell --base fedora --candy my-tool
charly -C my-project box validate                              # the gate
charly -C my-project box build my-shell                        # a fresh box, not a patched one
```

`-C` names the project each verb acts on, so the sequence runs from anywhere. `--repo` is the
read-only counterpart — it resolves a *published* project into a cache, so a scaffold written to
your project is invisible to it. Editing verbs and `--repo` never belong in the same sequence.

Five commands, a few minutes, and the recipe still describes exactly what is running. Compare the
alternative — installing the tool inside the running container by hand — which works until the
next `charly update` destroys it, and which leaves the box's own definition quietly wrong in the
meantime.

The scaffolded candy will not pass the gate until it declares what it does and how to check it, so
even the quick fix arrives documented and verifiable. That is
[the schema gate](/concepts/07-the-schema-comes-first/) doing its job.

## See also

- **[Disposability is the license](/concepts/09-disposability-is-the-license/)** — why the rebuild is cheap.
- **[Authoring a candy](/guides/authoring-a-candy/)** — the scaffold-to-composed walkthrough.
- **[One recipe, many molds](/concepts/02-one-recipe-many-molds/)** — what you are editing.
