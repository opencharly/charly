---
title: One recipe, many molds
description: A candy list composes into an image — and the same list installs onto a host, a VM, a cluster, or a phone.
sidebar:
  order: 2
---

> **One recipe, many boxes.** A single declarative recipe — candies stacked into a box — pours
> into every mold: an interactive shell, a managed pod, a host workstation, a kubernetes cluster, a
> bootable VM, an Android device. Write the recipe once; let `charly` set it in whatever shape
> the moment needs.
>
> — [tenet 2](/vision/)

## The idea

A **candy** installs one concern. A **box** is a candy that also carries a `base:`, plus a list of
other candies to compose. Composition is transitive and topologically sorted: `require:` expresses
ordering ("this must be installed first"), `candy:` expresses composition ("splice these in here").

The distinction between a candy and a box is deliberately thin — a box *is* a candy, just one that
names a base. That thinness is what makes the second half of the tenet possible. Because a box is
only a candy list plus a starting point, the same list can be applied somewhere that has no image
at all: a host, a VM guest, a phone. You are always applying candies; only the substrate changes.

Every substrate consumes the same intermediate representation, so adding one does not add a
vocabulary. Reversal is part of that IR rather than bolted on per substrate: a step can record the
operation that undoes it. You see this most directly on the host target, where those recorded
operations go into an install ledger and `charly fleet del host` replays it backwards instead of
making a best-effort guess at cleanup.

## In practice

The whole model in one worked example. First a **layer** — one concern, and the probes that prove
it ([`ripgrep`](/recipes/tools/ripgrep/), quoted from its `charly.yml` in [opencharly/layer-ripgrep](https://github.com/opencharly/layer-ripgrep)):

```yaml
ripgrep:
    candy:
        version: 2026.144.1443
        description: |
            Fast recursive text search (rg)
            ...
        package:
            - ripgrep
        plan:
            - check: the rg binary is installed at /usr/bin/rg
              file:
                file: /usr/bin/rg
                exists: true
```

Then a **box** — the same `candy:` keyword, plus `base:` and a composition list. This is
[`tutorial-shell`](/reference/box/fedora/tutorial-shell/), quoted from
`box/fedora/box/tutorial-shell/charly.yml`, and it is the box the rest of this site uses:

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

Two candies, and they are the two kinds you will meet: `ripgrep` is a **tool** layer (packages and
probes, no service); `sshd` is a **service** layer.

Notice what the list does *not* contain: an init. `sshd` declares a service, so charly resolves
whichever init this target needs and brings it in — supervisord for a container, nothing extra for
a systemd machine, since systemd is already the init there. **You declare the service; the init
follows, per target.** That is why the same two-candy list is correct for a pod and for a VM guest.

The box's single check is worth reading closely, because it shows where a check *belongs*. It does
not assert that `rg` and `sshd` are both present — each candy's own plan already proves that, and
both plans run against this same image. It asserts the one thing composition itself produced: that
`sshd` became a *supervisord program*. Neither candy can claim that alone. Put a check on the
behaviour's provider; put it on the composing box only when the claim is about the composition.

Build it, enter it, run it, prove it:

```bash
charly --repo opencharly/distro-fedora box validate                    # the schema gate — nothing runs until it passes
charly --repo opencharly/distro-fedora box build tutorial-shell        # → multi-stage Containerfile → image
charly --repo opencharly/distro-fedora shell tutorial-shell            # → you are inside the candybox
charly --repo opencharly/distro-fedora check run check-tutorial-shell    # → build, deploy, probe, fresh rebuild, tear down
```

### The payoff: change the mold, keep the recipe

The deploy names a substrate. Swap it and the same candies land somewhere else entirely:

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

`check-group-app` is a `kind: local` template composing a candy that drops a marker file. The
member is the nested `local:` deploy; the `vm:` above it is the disposable guest it lands in, and
the bed asserts the marker appears **in the guest**.

The nesting is the point. A `local:` deploy installs packages and systemd units onto whatever
machine it targets, so the honest way to demonstrate it — and the way this repository's own beds do
it — is to point it at a disposable guest. Run it against your workstation only when you actually
mean to change your workstation.

| Substrate | Kind | What it means |
|---|---|---|
| your workstation | `local:` | candies applied to a host, over a shell or SSH |
| a virtual machine | `vm:` | libvirt/QEMU guests, candies applied inside over SSH |
| a cluster | `kubernetes:` | generated Kustomize manifests |
| a phone | `android:` | apps installed onto a device over adb |
| a container | `pod:` | the managed-pod case |

No second vocabulary. That is the whole tenet.

### The third role: a candy can extend charly itself

A candy carrying a `plugin:` block does not install software into a box — it teaches `charly` a
new verb, kind, or command. [`plugin-example`](/reference/candy/plugin-example/) is the canonical
one:

```yaml
plugin-example:
    candy:
        version: 2026.176.1400
        description: |-
            Reference plugin candy for the `exampleprobe` check verb ...
        plugin:
            source: github.com/opencharly/charly/candy/plugin-example
            providers:
                - verb:exampleprobe
```

Same file format, same validation, same acceptance plan as any other candy. See
[authoring a plugin](/guides/authoring-a-plugin/) for the full model.

:::tip[Yes, it comes with the kitchen sink]
The catalog does not stop at two candies. At the other end of the spectrum sit the
**kitchen-sink dev boxes** — [`fedora-coder`](/recipes/coder/fedora-coder/) and its
[`arch`](/recipes/coder/arch-coder/), [`debian`](/recipes/coder/debian-coder/) and
[`ubuntu`](/recipes/coder/ubuntu-coder/) siblings — around thirty candies each: five AI coding
CLIs, every language runtime, the DevOps tooling, nested containers, rootless VMs. Same recipe
format as the two-candy box above.
:::

## See also

- **[The words](/concepts/00-vocabulary/)** — candy, box, candybox, plus polymorphism and nesting.
- **[Candy reference](/reference/candy/sshd/)** — every defined candy, with its acceptance plan.
- **[Box reference](/reference/box/fedora/tutorial-shell/)** — every defined box and what it composes.
- **[The spec is the test](/concepts/06-the-spec-is-the-test/)** — what those `check:` steps are.
