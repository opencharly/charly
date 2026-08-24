---
title: The spec is the test
description: What a candy should do is written as a runnable acceptance plan, baked into the image and executed against the live deployment.
sidebar:
  order: 6
---

> **Write down what "good" means, and have an agent taste it — Agent Driven Evaluation.** The
> plan's deterministic `check:` steps verify the measurable; for the subtle "is it actually
> right?" an `agent-check:` step has an agent taste the live batch with the full probe kit and
> judge.
>
> — [tenet 6](/vision/)

## The idea

A specification that is only prose decays quietly. Nothing executes it, so nothing tells you the
day it stops being true.

Every candy here writes its specification as a runnable **plan** instead — an ordered list of
steps, each carrying exactly one intent:

| Intent | Meaning |
|---|---|
| `run:` | changes state (the install timeline) |
| `check:` | an idempotent probe — deterministic, and the mandatory minimum |
| `agent-run:` | an agent-performed action that may mutate |
| `agent-check:` | an agent grades the live deployment; an unparseable or timed-out grader **fails** the step |
| `include:` | compose another entity's plan |

Two properties make this more than a test suite. First, it is **mandatory**: every candy must ship
a non-empty `description:` and at least one deterministic `check:` step, or `charly box validate`
refuses the project. There is no such thing here as a candy nobody can verify.

Second, the plan is **baked into the image** as an OCI label, so it travels with the artifact. A
pulled image can be checked without its source repository — the thing that claims a behaviour also
carries the means to test that claim.

The `agent-check:` steps cover what deterministic probes cannot express — "does the desktop
actually render correctly", "can a client really open a session". An agent probes the live
deployment and judges. That half stays opt-in; the deterministic half never is.

## In practice

Here is a real plan, quoted from the ripgrep candy's `charly.yml` ([opencharly/layer-ripgrep](https://github.com/opencharly/layer-ripgrep)). Note that it does not merely assert
the binary exists — it asserts the tool *behaves*, including the negative case:

```yaml
        plan:
            - check: the rg binary is installed at /usr/bin/rg
              file:
                file: /usr/bin/rg
                exists: true
            - check: rg reports a parseable ripgrep version on stdout
              exit_status: 0
              stdout:
                - matches: "ripgrep [0-9]"
              command: rg --version
            - check: rg prints the matching line for a pattern present in piped input
              exit_status: 0
              stdout:
                - contains: beta
              command: printf 'alpha\nbeta\ngamma\n' | rg beta
            - check: rg exits 1 (no match) when the pattern is absent, never matching spuriously
              exit_status: 0
              command: printf 'alpha\nbeta\n' | rg ZZZ-no-such-pattern; test $? -eq 1
```

Run the plan against the built image, in a disposable throwaway container:

```bash
charly --repo opencharly/distro-fedora check box tutorial-shell
```

```
  PASS  check the rg binary is installed at /usr/bin/rg
  PASS  check rg reports a parseable ripgrep version on stdout  exit=0
  PASS  check rg prints the matching line for a pattern present in piped input  exit=0
  PASS  check rg exits 1 (no match) when the pattern is absent, never matching spuriously  exit=0
  ...
  SKIP  check service=sshd  context [runtime] not active in box mode

24 steps: 19 passed, 0 failed, 5 skipped
```

Steps marked `context: [runtime]` skip here by design — a service cannot be running in an image.
Run the same plan against a *live* deployment and they execute, with deploy-time variables
(`${HOST_PORT:N}`, `${VOLUME_PATH:name}`) resolved at execution time so one check survives port
remaps:

```bash
charly check live <deployment>
```

## See also

- **[Prove the risky thing first](/concepts/05-prove-the-risky-thing-first/)** — the co-equal twin.
- **[The verb catalog](/recipes/check/check/verb-catalog/)** — every deterministic probe.
- **[Live probe verbs](/recipes/check/check/live-probe-verbs/)** — the browser, Wayland, D-Bus, VNC and cluster probes.
