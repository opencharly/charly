---
title: Boxes all the way down
description: One of the things a recipe can build is charly itself — running inside one of its own disposable boxes.
sidebar:
  order: 12
---

> **The factory fits in a box, too — candyboxes all the way down.** One of the molds a recipe can
> pour into is the *factory itself*: the whole `charly` line, nested inside one of its own
> disposable candyboxes. A candybox forged inside a candybox: that nesting is the whole trick.
>
> — [tenet 12](/vision/)

## The idea

Every page before this one describes something charly does to *other* software. This one closes
the loop: `charly` is itself just another thing a recipe can install, so one of the molds a recipe
pours into is the toolchain that pours the molds.

The consequence is that the whole build-deploy-prove cycle can run *one level in* — from inside a
disposable outer candybox, building fresh candyboxes, testing them on live deployments, and
melting the failures back down. Because the outer box is as throwaway as the ones it builds, the
line can prove and rebuild itself as freely as it proves anything else.

Two properties make the nesting real rather than a diagram. Rootless nested containers work
without additive capabilities, and so do rootless libvirt VMs — so the inner level is not a
privileged escape hatch, it is the same uid-1000 arrangement one level down. And the evaluation
verdict can *drive* the next iteration: a bed can carry an `iterate:` block, handing the loop to
an agent that reads the scope and the previous results, rebuilds, redeploys, and is re-scored
until it plateaus or a watchdog fires.

That is what "verification becomes self-hosting" means concretely. The thing being tested and the
thing doing the testing are the same tool, at two levels of the same nesting.

## In practice

Boxes that carry the charly toolchain are ordinary boxes:

```bash
charly --repo opencharly/distro-fedora box build charly-fedora     # charly, built from source, inside a box
charly --repo opencharly/distro-fedora check run check-charly-fedora-pod
```

Inside that candybox, `charly` behaves exactly as it does outside — it builds boxes, launches
nested rootless pods, and creates libvirt VMs, all at uid 1000 with no `--privileged`. The
[`openclaw-desktop`](/recipes/openclaw/openclaw-desktop/) box goes furthest: a full streaming
desktop with agent CLIs and a nested `charly`, reachable in a browser, where an agent can drive
the entire loop from a terminal inside the candybox.

The agent-driven half is declared on a bed. An `iterate:` block names the eligible agent CLIs, the
sandbox the agent and `charly` run in, the prompt, the plateau count and the watchdog — and the
bed's own `plan:` is the scored content. `charly check run` then runs the loop instead of a single
pass.

## Where next

That is the twelve. From here:

- **[The vision](/vision/)** — the tenets in their original form, plus where this is heading.
- **[Quickstart](/start/quickstart/)** — if you have been reading rather than running.
- **[Recipe cards](/recipes/)** — the catalog, now that the vocabulary means something.

## See also

- **[The box is the boundary](/concepts/01-the-box-is-the-boundary/)** — where the tour started.
- **[Disposability is the license](/concepts/09-disposability-is-the-license/)** — why nesting is safe to iterate in.
- **[Check beds and the acceptance gate](/recipes/check/check/beds-and-r10/)** — the `iterate:` harness.
