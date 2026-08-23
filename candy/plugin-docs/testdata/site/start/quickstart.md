---
title: Quickstart
description: Read a real box, build it, enter it, and prove it works — four commands, one config file.
sidebar:
  order: 2
---

Everything below acts on one file. Read it first — building something you have not seen is how
charly stops making sense three commands in.

## The file

This is [`tutorial-shell`](/reference/box/fedora/tutorial-shell/), excerpted from
`box/fedora/box/tutorial-shell/charly.yml` — everything but the long `description:` body and the
explanatory comments. It is a real box in the repository, and a
[check bed](/concepts/09-disposability-is-the-license/) re-proves it on every acceptance run:

```yaml
tutorial-shell:
    candy:
        description: |-
            The teaching box behind opencharly.ai's quickstart — a minimal, real dev shell
            ...
        base: fedora
        candy:
            - '@github.com/opencharly/charly/candy/ripgrep:v2026.201.0706'
            - '@github.com/opencharly/charly/candy/sshd:v2026.201.0706'
        plan:
            - check: composing the service candy next to the init candy wired sshd into the assembled supervisord config — a program block neither candy produces on its own
              id: tutorial-shell-service-wired-into-init
              file:
                file: /etc/supervisord.conf
                contains:
                    - contains: "[program:sshd]"
```

Four things are going on, and they are the whole model:

- **`candy:`** is the only entity keyword. This node carries `base:`, which makes it a **box** — a
  buildable container image. A node without `base:` would be a layer instead.
- **`base: fedora`** points at another box defined next door, not an external registry image. A
  base can be either.
- **the `candy:` list** composes two candies: a tool (`ripgrep`) and a service (`sshd`). Each
  installs one concern — and note what is *absent*: an init. Because `sshd` declares a service,
  charly resolves the init this target needs and brings it in for you (supervisord in a container;
  nothing extra on a systemd machine, which already has one). You declare the service; the init
  follows.
- **`plan:`** is the acceptance spec, and it is mandatory. Note *what* it checks: not that `rg` and
  `sshd` are present — each candy's own plan already proves that, and those plans run against this
  same image — but that composing the service candy next to the init candy made `sshd` a
  **supervisord program**. A check belongs on the behaviour's provider; it belongs on the composing
  box only when the claim is about the composition itself.

New to the vocabulary? [The words](/concepts/00-vocabulary/) defines candy, box and candybox in
five minutes.

## Build it

```bash
charly --repo opencharly/distro-fedora box validate              # the schema gate — silence is the pass
charly --repo opencharly/distro-fedora box build tutorial-shell
```

`box build` resolves the candy graph, generates a multi-stage Containerfile, and builds the image.
The acceptance plan of every composed candy is baked into the result as an OCI label, so the image
can be tested later without this repository.

The `--repo opencharly/distro-fedora` says which project to read, fetched and cached for you — so
this works with nothing but the `charly` binary installed, no clone required. If you *do* have the
repository checked out, `-C box/fedora` points at the same project on disk.

## Enter it

```bash
charly --repo opencharly/distro-fedora shell tutorial-shell
```

You are now inside the **candybox** — the running, isolated form of that box. There is no command
filter: `dnf` works, `rg` works, anything you install works. It runs rootless at uid 1000 with no
`--privileged`. [Why that is the safe arrangement →](/concepts/01-the-box-is-the-boundary/)

## Prove it

```bash
charly --repo opencharly/distro-fedora check box tutorial-shell     # run the baked plan against the image
```

```
24 steps: 19 passed, 0 failed, 5 skipped
```

The five skips are three different things, and the distinction is worth reading:

| Skipped | Why |
|---|---|
| `sshd-service-running`, `sshd-port-reachable`, and supervisord's control-socket check | `context: [runtime]` — a service cannot be running inside an image |
| a `run:` step that grants the uid-1000 account passwordless sudo | it mutates, and `check box` is verify-only |
| an `agent-check:` step | it is graded by an agent, and no grader is bound here |

Only the first group needs a deployment. That is what the bed provides:

```bash
charly --repo opencharly/distro-fedora check run check-tutorial-shell
```

```
[image-build]         PASS after 36s
[check-image]         PASS after 17s
[deploy-add]          PASS after 16s
[start]               PASS after 9s
[check-live]          PASS after 16s
...
[update]              PASS after 63s
[check-live-rebuild]  PASS after 16s
[cleanup]             PASS after 6s
PASS (steps=13)
```

Eight of the thirteen steps are shown; `...` marks the elided ones (`config`,
`bring-up-members`, `feature-run`, `feature-run-rebuild`, `cleanup-members`).

One command: build, deploy, bring to steady state, run the plan, then **destroy and rebuild from
scratch** and run it again, then tear everything down. That second `check-live-rebuild` is the
interesting one — it is what separates "it worked once" from "it reproduces".

Rebuilding from scratch on every run is affordable only because the deploy is declared
`disposable: true` — charly may destroy it without asking, so proving reproducibility costs one
flag rather than a separate environment.

## Change the mold

The same candies are not tied to containers. A `local:` deploy applies them to a *machine* — and
because that means real packages and real units on whatever it targets, this repository
demonstrates it against a disposable VM guest rather than a workstation:

```yaml
# charly.yml — a local: deploy nested INSIDE a disposable VM guest, so the
# "machine" it changes is the guest and never yours
check-group:
    group:
        disposable: true
        lifecycle: dev
        ...
    check-group-vm:
        vm:
            from: eval-vm
        check-group-member:
            local:
                from: check-group-app
```

Tree position is what makes it safe: because `check-group-member` is nested *under* the `vm:`
node, the `local:` deploy lands inside the guest rather than on the host. It carries no `host:`
field at all — the venue comes from its parent. `check-group-app` is itself a `local:` template
composing one candy that drops a marker file, and the bed asserts that marker exists **in the
guest**.

Point a `local:` deploy at your own machine only when you actually mean to change your own
machine.

Swap the substrate and the same list installs into a VM guest over SSH, generates Kubernetes
manifests, or installs apps onto a phone. No second vocabulary.

:::caution[Point `local:` at a candybox, not at yourself]
A `local:` deploy installs packages and systemd units onto whatever machine it targets. Nested in a
disposable guest, as above, that is free to experiment with. Targeting `host: local` changes the
machine you are sitting at — do that when you mean to, not while following a tutorial.
:::

[One recipe, many molds →](/concepts/02-one-recipe-many-molds/)

## Where to go next

- **[The words](/concepts/00-vocabulary/)** — the vocabulary, defined once.
- **[The concepts tour](/concepts/01-the-box-is-the-boundary/)** — twelve short pages, in the order
  the project's own [vision](/vision/) states them.
- **[Authoring a candy](/guides/authoring-a-candy/)** — start your own project.
- **[Recipe cards](/recipes/)** — the dedicated page for every candy, box and verb.
