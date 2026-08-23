---
title: The schema comes first
description: The shape of everything you can author is defined once in CUE — and the code, the validation gate, and these pages are all generated from it.
sidebar:
  order: 7
---

> **Carve the master pattern before building the machine — Schema Driven Design.** The shape of
> everything the factory accepts is carved ONCE, as a master pattern, before any machinery is
> built to handle it. Nothing is copied freehand beside the pattern — a freehand copy drifts; a
> casting cannot.
>
> — [tenet 7](/vision/)

## The idea

Configuration formats usually accrete. A field is added to the parser, then to the validator, then
to the documentation, then to the migration — four hand-maintained copies of one fact, drifting
apart at four different rates. The failure mode is familiar: a field the parser accepts, the
validator ignores, and the docs never mention.

Here the shape is defined once, in CUE — a schema language whose name stands for *Configure, Unify, Execute* — and everything that needs it is **generated** from that
single source: the Go types the code works with, the validation gate at the project door, the
migration that modernises old configs, and the parameter tables you can read on this site.

The rule is absolute in one direction — schema-shaped Go is generated, never hand-transcribed.
Regeneration on a clean tree must be a no-op, and drift is treated as an incident rather than
housekeeping. Changing the schema is a CUE edit followed by regeneration; the code follows from
the pattern rather than being kept in step with it by hand.

The same principle produces the pages you are reading. A plugin's `schema/*.cue` is the single
source that generates its Go parameter types, answers the runtime `Describe` RPC, *and* renders
its parameter reference here. All three cannot disagree, because there is only one of them.

Validation is a **gate**, not a linter: every document is checked against one closed schema
*before* it executes. A document that does not conform does not run.

## In practice

The gate runs before anything else, and it is the first command in any workflow:

```bash
charly --repo opencharly/distro-fedora box validate
```

Its silence is the pass. When it does speak, it speaks precisely — a field that is not in the
schema is rejected by name rather than quietly ignored, which is what makes a typo a five-second
problem instead of a debugging session.

The mandatory-field rules from [the spec is the test](/concepts/06-the-spec-is-the-test/) are
enforced right here: a candy without a `version:`, without a non-empty `description:`, or without
at least one deterministic `check:` step fails validation. The catalog on this site can promise
that every candy is documented and verifiable precisely because the gate refuses the alternative.

Older projects come forward in one idempotent pass:

```bash
charly migrate
```

There is no migration ladder to walk and no ordering to get right — it is a single chain to the
current schema version, and running it twice changes nothing.

## See also

- **[The migrate command](/recipes/build/migrate/)** — the schema floor and the migration table.
- **[Validate](/recipes/build/validate/)** — the gate's rules in detail.
- **[Reproducible, not merely successful](/concepts/08-reproducible-not-merely-successful/)** — versions and pins.
