---
title: Prove the risky thing first
description: Documentation and code are hypotheses. When being wrong would invalidate the plan, prove it on a live bed before designing around it.
sidebar:
  order: 5
---

> **Taste every candy before making the recipe — Risk Driven Development.** The riskiest question
> — *do these candies actually melt together the way I think they do* — gets proven on a real,
> disposable candybox first. Reality is the only ground truth.
>
> — [tenet 5](/vision/)

## The idea

Every plan rests on assumptions, and they are not equally dangerous. Most are cheap to be wrong
about — you find out, you adjust. A few are load-bearing: if that one is wrong, the design built
on top of it was never viable, and you discover that after the expensive part is done.

Risk Driven Development is a sequencing rule for exactly those: **the riskiest unknown gets proven
first, on a live disposable candybox, before the work is designed around it.** Documentation and
source code are treated as hypotheses — good ones, usually, but not evidence. When being wrong
would invalidate the plan, a running system is what settles it.

The archetypal load-bearing unknown in this project is composition at current versions: *do these
particular candies, at the versions they resolve to today, actually build and run together?* No
amount of reading answers that. One bed run does.

The instrument is a **spike**: small, time-boxed, and thrown away once it has answered the
question. Only the knowledge is kept, never the artifact. A spike finds out *how*; it never
decides *whether*, and it never shrinks the work. If a spike refutes the mechanism you had in
mind, the answer is to find the next mechanism — not to reduce the goal.

There is a corollary that keeps the rest of this site honest: **when a bed contradicts a document,
the document is wrong**, and it gets fixed the same day. That rule is the reason the reference
half of this site is generated rather than written — a page projected from the source cannot
disagree with it.

## In practice

The whole point is that proving is cheap enough to do before committing. One command runs the
full sequence — build, deploy, bring to steady state, run the acceptance plan, rebuild from
scratch, prove it again, tear everything down:

```bash
charly --repo opencharly/distro-fedora check run check-tutorial-shell
```

Compose the thing you are unsure about, point a `disposable: true` deploy at it, and run that
before the design depends on the answer. The bed backing the example on this site is exactly that
shape — quoted from `box/fedora/charly.yml`:

```yaml
check-tutorial-shell:
    pod:
        image: tutorial-shell
        disposable: true
        lifecycle: dev
```

That is the whole of its deploy configuration — the node also carries a `description:`, elided here
for length. What it carries **no** amount of is a `plan:` of its own, and the reason is the useful
part: a bed's acceptance content is the *composed candies' own* probes, which already run against
the live deployment. A bed adds steps only for a claim that needs a live deployment **and** that no
composed candy already makes — elsewhere in that same file, `check-sway-browser-vnc-pod` does
exactly that, adding browser and desktop probes against a running desktop.

So what does running it prove? Not a step written here — the *sequence*: build the image, deploy
it, reach steady state, run every composed candy's plan against the live pod, then destroy and
rebuild it and run them all again. The riskiest assumption in that chain is whether these candies,
at the versions they resolve to today, compose and come up together at all. No step you could
author would answer that; only running it does.

## See also

- **[The spec is the test](/concepts/06-the-spec-is-the-test/)** — RDD's co-equal twin.
- **[Disposability is the license](/concepts/09-disposability-is-the-license/)** — why running beds is cheap.
- **[Check beds and the acceptance gate](/recipes/check/check/beds-and-r10/)** — the bed model in full.
