---
title: Authoring a candy
description: Start your own project, add a candy that installs one concern, compose it into a box, and prove it.
sidebar:
  order: 1
---

The [quickstart](/start/quickstart/) reads a box that already exists. This page writes one.

## Your project's layout

A charly project is a `charly.yml` plus two discovery directories:

```
my-project/
├── charly.yml              # the project entry point — deploys live here
├── candy/
│   └── my-tool/
│       └── charly.yml      # a LAYER candy — one concern
└── box/
    └── my-shell/
        └── charly.yml      # a BOX — a candy carrying base:
```

Scaffold it:

```bash
charly box new project my-project
```

Every command below names that project with `-C`, so none of them depends on which directory you
are standing in.

`charly.yml` is the only filename charly knows. Everything else — which files to `import:`, which
directories to `discover:` — is configured there.

:::note[Where the repository's own boxes live]
In the opencharly repository itself, boxes live in the `box/<distro>` git submodules rather than
the main repo, which is why examples on this site say `charly --repo opencharly/distro-fedora box build …`. Your own
project has no such split: `box/<name>/charly.yml` in your project root is the normal layout, and
plain `charly box build <name>` finds it.
:::

## A candy that installs one concern

```bash
charly -C my-project box new candy my-tool
charly -C my-project candy add-rpm my-tool ripgrep
```

The editor verbs go through the YAML *node* API, so comments and key order survive every edit —
which is what makes them safe for an agent to drive. You can equally hand-edit the file.

Three fields are mandatory on every candy, and the gate enforces all three: a CalVer `version:`, a
non-empty `description:`, and a `plan:` carrying at least one deterministic `check:` step. That is
not ceremony — it is what makes the [catalog](/recipes/) and
[the spec is the test](/concepts/06-the-spec-is-the-test/) true rather than aspirational.

A complete, real one — this is [`ripgrep`](https://github.com/opencharly/layer-ripgrep), quoted from
its `charly.yml`:

```yaml
ripgrep:
    candy:
        version: 2026.144.1443
        description: |
            Fast recursive text search (rg)
            Installs the ripgrep package, which provides the `rg` binary at
            /usr/bin/rg — a fast recursive grep that honours .gitignore by
            default. ...
        package:
            - ripgrep
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
```

Write the `description:` for a stranger — it is published verbatim as that candy's card wherever
the candy's project is published, and it is baked into every image that composes the candy.

### Per-distro packages

Package names differ across distros, so declare them under `distro:` and let the resolver cascade
most-specific-first:

```yaml
        package:                  # the base — installed on every distro
            - git
        distro:
            fedora:
                package: [ripgrep]
            arch:
                package: [ripgrep]
            "debian,ubuntu":      # compound — shared by both
                package: [ripgrep]
```

Python belongs in `pixi.toml`, npm in `package.json`, Rust in `Cargo.toml` — drop the manifest in
the candy directory and the builder stage is detected automatically. Do not reach for
`command: pip install`.

## Compose it into a box

```bash
charly -C my-project box new box my-shell --base fedora --candy my-tool
```

Or write it directly — a box is the same `candy:` keyword plus a `base:`:

```yaml
my-shell:
    candy:
        description: A minimal dev shell with my tool.
        base: fedora
        candy:
            - my-tool
```

## Build and prove

```bash
charly -C my-project box validate            # the gate — silence is the pass
charly -C my-project box build my-shell
charly -C my-project check box my-shell      # runs the baked plan in a disposable container
```

To prove the deployed behaviour too, declare a disposable bed in `charly.yml` and run it:

```yaml
check-my-shell:
    pod:
        image: my-shell
        disposable: true
        description: Disposable bed for my-shell.
```

```bash
charly -C my-project check run check-my-shell
```

`disposable: true` is what authorizes charly to destroy and rebuild the deployment unattended. It
is never inferred — see [disposability is the license](/concepts/09-disposability-is-the-license/).

## Two rules worth internalising early

**Prefer the declarative verbs over `command:`.** `mkdir:`, `copy:`, `write:`, `link:`,
`download:` and `setcap:` all exist; `command:` is the escape hatch. In particular `write:` takes
inline `content:` and stages it as a file, so you never need a shell heredoc.

**Never split a service into `-host` and `-pod` sibling candies.** A candy that needs the same
service under both supervisord and systemd declares *both forms in one `service:` list*, and the
init system at deploy time picks. [`sshd`](/recipes/coder/sshd/) is the canonical example, and it
is one of the two candies in the box the quickstart reads.

## Next

- **[The layer reference](/recipes/image/layer/)** — the full field and verb catalog.
- **[The check verb catalog](/recipes/check/check/verb-catalog/)** — every deterministic probe.
- **[Authoring a plugin](/guides/authoring-a-plugin/)** — a candy's third role.
