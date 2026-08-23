---
title: Disposability is the license
description: A box whose destruction costs nothing can be handed over completely — which is what makes autonomous iteration safe.
sidebar:
  order: 9
---

> **Every spoiled batch is a new lesson waiting to be learned.** Every candybox is both a testbed
> and the recipe for the final product by explicit design, so a failed batch costs nothing but the
> lesson inside it. A failure is feedback to be mined, never an incident to be prevented at all
> costs — and that is exactly what lets autonomous iteration be *fearless* and *safe* at once.
>
> — [tenet 9](/vision/)

## The idea

[Tenet 1](/concepts/01-the-box-is-the-boundary/) says hand over the whole toolset and trust the
walls. That is only tolerable if being wrong is cheap. Disposability is what makes it cheap: a
candybox whose destruction costs nothing can be handed over completely, because the worst outcome
is a rebuild.

So disposability is a **first-class, explicitly declared property**, and the declaration is
load-bearing:

```yaml
check-tutorial-shell:
    pod:
        image: tutorial-shell
        disposable: true
```

`disposable: true` is the one and only authorization for autonomous destroy-and-rebuild. It is
never inferred — not from a name, not from a hostname, not from a lifecycle tag. That refusal to
infer is the entire safety property, and it comes from the obvious failure mode: inference is
exactly how an "obviously throwaway" machine turns out to have been someone's staging environment.

Note where the flag lives. It is a property of the **deploy**, not of the image. The same box can
back a disposable bed and a production deployment; only the deploy that opted in may be destroyed.

The second half of the tenet is about what you do with a failure. A spoiled batch is not an
incident to be prevented at all costs — it is feedback, and the cheapest possible feedback, since
the artifact was throwaway by construction. That is what makes autonomous iteration reasonable
rather than reckless: an agent that can destroy and rebuild freely, but only where a human wrote
`disposable: true`.

## In practice

On a disposable deploy, the destroy-and-rebuild cycle runs unattended:

```bash
charly --repo opencharly/distro-fedora update check-tutorial-shell   # destroy → rebuild → recreate → start
```

Run the same command against a deploy without the flag and charly will not do it unprompted. There
is no override that infers intent from context — the authorization is the declaration.

This is also what makes the acceptance gate affordable. Every bed run ends by destroying what it
built:

```bash
charly --repo opencharly/distro-fedora check run check-tutorial-shell
```

```
...
[update]              PASS after 63s
[check-live-rebuild]  PASS after 16s
[cleanup]             PASS after 6s
PASS (steps=13)
```

Only the tail is shown — `...` stands for the nine earlier steps that build, deploy and probe the
box before this destroy-and-rebuild leg.

Five minutes, and nothing survives it. A verification you can run that freely is one you actually
run.

## See also

- **[The box is the boundary](/concepts/01-the-box-is-the-boundary/)** — what disposability licenses.
- **[Disposable-flag semantics](/recipes/internals/disposable/)** — the authorization rules in full.
- **[Rebuild beats patch](/concepts/11-rebuild-beats-patch/)** — the design consequence.
