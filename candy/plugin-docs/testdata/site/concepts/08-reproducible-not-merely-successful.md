---
title: Reproducible, not merely successful
description: A build that works once is not the goal — a build that produces the same result on the next rebuild is.
sidebar:
  order: 8
---

> **Conched smooth, then tempered to set.** A candybox is brought to one correct, reproducible
> state and held there: its version read from its own contents, so it stays put whenever nothing
> changed; its candies nucleating around one matching set of versions instead of a half-pinned
> jumble.
>
> — [tenet 8](/vision/)

## The idea

"It builds" and "it builds the same way tomorrow" are different claims, and only the second is
worth anything. The failure this engineers out is the box that builds today and drifts tomorrow —
green once, subtly different on the rebuild, and nobody noticed because nobody rebuilt.

Three mechanisms hold a box in one state.

**Content-derived versions.** An image's version label is derived from what it actually contains,
not stamped from the clock. Rebuild a box whose inputs did not change and the label does not move.
That makes "did anything actually change?" a question with an answer, rather than a diff of two
timestamps.

**Per-entity CalVer identity.** Every candy carries its own `version:` in `YYYY.DDD.HHMM` form.
That version — not the git tag it was fetched with — is the candy's identity. A repository re-tag
that leaves a candy's content alone therefore resolves silently to the same thing, while two
genuinely different versions of one candy produce a warning naming both.

**Pin alignment.** When repositories drift apart, the pins that reach into them drift too.
Alignment is a command rather than an archaeology exercise.

And the acceptance run closes the loop: a bed does not stop at "deployed and healthy". It destroys
the deployment, rebuilds it from scratch, and proves it healthy *again* — because a state that
only holds on the first attempt never really held.

## In practice

Build a box twice with nothing changed in between, and the version label stays put:

```bash
charly --repo opencharly/distro-fedora box build tutorial-shell
charly --repo opencharly/distro-fedora box inspect tutorial-shell
```

When a warning does appear — `referenced at multiple versions` — it is telling you two pins
disagree about which version of a candy this project uses. Align them:

```bash
charly --repo opencharly/distro-fedora box reconcile
```

A warning is never an acceptable end state here. It is the mechanism reporting exactly the
half-pinned jumble the tenet is about.

The rebuild half is not something you opt into — it is built into every bed run. `charly check run`
deploys, proves, then runs a fresh `charly update` (destroy, rebuild, recreate) and proves the
whole plan *again* before tearing down:

```
[check-live]          PASS after 16s
[update]              PASS after 63s
[check-live-rebuild]  PASS after 16s
[cleanup]             PASS after 6s
PASS (steps=13)
```

That `check-live-rebuild` line is the tenet made mechanical. A box that passes the first
`check-live` and fails the second was never tempered — it was lucky.

## See also

- **[Reconcile](/recipes/build/reconcile/)** — aligning cross-repo version pins.
- **[Capabilities and OCI labels](/recipes/internals/capabilities/)** — where the derived version lives.
- **[Check beds and the acceptance gate](/recipes/check/check/beds-and-r10/)** — the full sequence.
