# OpenCharly

**The fully stocked gourmet kitchen for you and your agents.**

`charly` builds container images from a composable list of **candies**. That part will feel
familiar. The part that usually does not: *the same list* also installs onto a VM guest, a
Kubernetes cluster, your own workstation, or an Android device — and every piece of it carries a
runnable acceptance test, so you can prove a deployment works instead of assuming it.

> **New here?** Start at **[opencharly.ai](https://opencharly.ai)** — a quickstart, a twelve-part
> concepts tour, and a generated reference for every candy, box, plugin and verb.
> [VISION.md](VISION.md) is the one-page thesis.

## If you already run containers and VMs

| You already use | For | charly's equivalent |
|---|---|---|
| a Containerfile | building an image | a `candy:` list on a `base:` |
| docker-compose | several containers together | `kind: pod` — deployed as systemd quadlets |
| Packer / cloud-init | building a VM guest | `kind: vm` — *the same candy list* |
| Ansible / a setup script | configuring a host | `kind: local` — *the same candy list* |
| — nothing you have — | proving the deploy actually works | a disposable **check bed** |

That last row is the point. It is the line most toolchains leave empty.

## What it is reacting to

Four things about ordinary practice, and what charly does instead. The full argument — status quo,
mechanism, and where each claim stops being true — is in **[GRIEVANCES.md](GRIEVANCES.md)**.

| Grievance | charly's answer |
|---|---|
| **Config is per-artifact, so nothing composes.** A Containerfile describes one image; sharing means a base-image chain, or copy-paste. | Config is per **candy** — one concern, one directory, its own checks. A **box** composes any subset of 286 of them. Composition is a set, not a chain. |
| **The same software is described once per distro.** Package names drift, init systems differ, so you keep two Containerfiles or take on a whole config-management layer. | A candy declares packages once. `package: [ripgrep]` already works on Fedora, Arch, Debian and Ubuntu; a `distro:` map overrides only what genuinely differs. |
| **Containers and VMs are separate worlds.** Containerfile, Packer, cloud-init, Ansible — four languages, four execution models, and drift you cannot see. | `pod:`, `vm:`, `local:`, `k8s:` and `android:` compile to one shared InstallPlan IR. Changing the substrate keyword is the whole change. |
| **Isolation is opt-in, shallow, and stops one level down.** Rootless has friction so things run privileged; nesting means `--privileged` or a mounted docker socket. | Every box is built to run **rootless and nested** — rootless podman inside a rootless container at uid 1000, rootless libvirt as ordinary services. That is what makes **candyboxing** work: a VM inside a container, or the reverse. |

The fourth is load-bearing for the other three. Because the boundary is real and it nests, you can
hand an agent the entire toolset inside something disposable, instead of trying to whitelist which
commands it may run.

## The words

A small private vocabulary, and one pair people conflate. Full glossary:
[the words](https://opencharly.ai/concepts/00-vocabulary/).

| Term | What it is | What it is *not* |
|---|---|---|
| **candy** | one entry in a `charly.yml` — the **only** entity kind there is | not "a layer", not "a package" |
| **box** | a candy carrying `base:`/`from:` → a buildable **container image** | not the running thing |
| **candybox** | a box in its **running, isolated form** — container, VM, or check bed. **The security boundary.** | not the image, not the config |
| **check bed** | a deploy marked `disposable: true` — a candybox that exists to be destroyed | not a test file |
| **plan** | the ordered acceptance spec a candy carries, baked into the image as an OCI label | not a build script |
| **substrate** | where a deploy lands: `pod:` `vm:` `k8s:` `local:` `android:` | |

## One candy, three roles

There is one `candy:` keyword, and what you put inside it decides what it is:

| What it declares | What it is | How it's used |
|---|---|---|
| `package:` / `plan:` / `service:` | a **layer** — one concern | spliced into any box's `candy:` list |
| …plus `base:` or `from:` | a **box** — a buildable image | `charly box build <name>` |
| …plus a `plugin:` block | a **plugin** — a new verb, kind or command for `charly` itself | loaded on demand, or compiled in |

One recipe-card format describes an ingredient, a finished dish, and a new piece of kitchen
equipment. That is why the core stays tiny while the catalog grows — and why there is no second
vocabulary to learn.

## Sixty seconds

This is `box/fedora/box/tutorial-shell/charly.yml` — a real box, re-proven on every acceptance
run by the `check-tutorial-shell` bed:

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

`base:` points at another box defined next door — it can equally be a registry ref. The two candies
are the two kinds you will meet: `ripgrep` is a **tool** layer (packages and probes, no service),
`sshd` a **service** layer.

Note what is *not* listed: an init. `sshd` declares a service, so charly resolves the init this
target needs and installs it — supervisord for a container, nothing extra for a systemd machine,
because systemd is already there. You declare the service; the init follows.

The `plan:` is worth reading closely. It does *not* check that `rg` and `sshd` are present — each
candy's own plan proves that, and those plans run against this same image. It checks the one thing
**composition** produced: that `sshd` became a supervisord program. A check belongs on the
behaviour's provider; it belongs on the composing box only when the claim is about the composition.

```bash
charly --repo opencharly/distro-fedora box validate                    # the schema gate — nothing runs until it passes
charly --repo opencharly/distro-fedora box build tutorial-shell        # → multi-stage Containerfile → image
charly --repo opencharly/distro-fedora shell tutorial-shell            # → you are inside the candybox
charly --repo opencharly/distro-fedora check run check-tutorial-shell    # → build, deploy, probe, fresh rebuild, tear down
```

**Then change the mold and keep the recipe.** The same candy list applies to a VM guest over SSH, a
Kubernetes cluster, or a host — swap the substrate in the deploy, not the recipe. No second
vocabulary.

The host substrate (`local:`) is the one to try *inside a candybox first*: it installs packages and
units onto whatever machine it targets, so point it at a disposable VM guest rather than your
workstation. [How that is wired →](https://opencharly.ai/concepts/02-one-recipe-many-molds/)

## The lifecycle

| Reach for `charly` when you want to… | …and you get |
|---|---|
| compose a reproducible box from a candy list | a `candy:` with `base:`, `charly box build` |
| run one or more containers as a managed pod | `kind: pod`, `charly bundle add`, `charly start` |
| apply the same candies to a host, VM, cluster, or phone | `charly bundle add` + a substrate kind |
| prove a config actually works, end to end | a disposable check bed, `charly check run` |

One `charly.yml`, one box, one per-host overlay, and one check bed drive all four. The binary that
ties them together is also an MCP server, so an agent reaches every verb over the same RPC.

## Install

**Install `charly` as a native package.** That is how you use it, and everything else here — and
every page on [opencharly.ai](https://opencharly.ai) — assumes a machine with `charly` installed
and no charly checkout anywhere. Build the package once from a source tree, then install it with
your own package manager (requires Go 1.26+ and [go-task](https://taskfile.dev)):

```bash
task build:pkg:arch   && sudo pacman -U dist/*.pkg.tar.zst    # Arch / CachyOS / Manjaro
task build:pkg:fedora && sudo dnf install dist/*.rpm          # Fedora
task build:pkg:debian && sudo apt install ./dist/*.deb        # Debian / Ubuntu
```

The system-wide install step is always yours to run, never a side effect of building.

Once it is on your `$PATH`, `--repo` reads any published project without cloning it:

```bash
charly --repo opencharly/charly box list boxes
```

**Working *on* charly** is the other thing, and only that:

```bash
git clone --recurse-submodules https://github.com/opencharly/charly.git
cd charly
task build:binary       # builds ./bin/charly (CalVer-stamped) — NEVER installs to the host
./bin/charly box build
```

Every invocation against that checkout uses `./bin/charly`; each checkout or worktree gets its own,
with nothing shared between them. Full detail, including the `$HOME`-local portable binary and its
`$PATH`-shadowing caveat: [Install](https://opencharly.ai/start/install/).

## Yes, it comes with the kitchen sink

The two-candy box above is the small end. At the other sit the **kitchen-sink dev boxes** —
`fedora-coder` and its `arch`, `debian` and `ubuntu` siblings — around thirty candies each: five
AI coding CLIs, every language runtime, DevOps tooling, nested rootless containers, rootless
libvirt VMs, all at uid 1000 with no `--privileged`. Same recipe format, same four commands.

A fully stocked kitchen really does ship with the sink.

## Where to learn more

Everything factual about a candy, box, plugin or verb is **generated** from the sources in this
repository and published at [opencharly.ai](https://opencharly.ai) — so it cannot drift from the
code the way a hand-maintained copy in this file would.

| You want | Go to |
|---|---|
| the vocabulary | [The words](https://opencharly.ai/concepts/00-vocabulary/) |
| the ideas, in order, with runnable examples | [The concepts tour](https://opencharly.ai/concepts/01-the-box-is-the-boundary/) — twelve short pages |
| to build your first thing | [Quickstart](https://opencharly.ai/start/quickstart/) → [Authoring a candy](https://opencharly.ai/guides/authoring-a-candy/) |
| every command, flag and verb | [CLI reference](https://opencharly.ai/reference/cli/bundle/) + [The charly CLI](https://opencharly.ai/guides/the-cli/) |
| every candy and box | [Candy reference](https://opencharly.ai/reference/candy/ripgrep/) · [Box reference](https://opencharly.ai/reference/box/fedora/tutorial-shell/) |
| "what implements `cdp:`?" | [Provider index](https://opencharly.ai/reference/providers/) |
| something is broken | [Troubleshooting](https://opencharly.ai/guides/troubleshooting/) |
| the thesis | [VISION.md](VISION.md) · [opencharly.ai/vision](https://opencharly.ai/vision/) |
| dated history | each repo's [`CHANGELOG/`](CHANGELOG/README.md), one file per CalVer version |

## Works with Claude Code, Codex, and Kimi

The bundled [plugins/](plugins/) directory provides one skill tree for Claude Code, Codex, and
Kimi, teaching each harness how to compose, build, deploy, check, and manage boxes. Every candy,
box, command, and contributor subsystem has an owning skill.

```bash
./plugins/setup claude                   # full developer mode (default)
./plugins/setup codex developer
./plugins/setup kimi developer
./plugins/setup codex user               # use and author with Charly
```

Setup writes project files only — it never changes `~/.claude`, `~/.codex`, `~/.kimi-code`, or any
other user configuration, and it does not depend on MCP.

Beyond skills, the project ships reusable plugin agents (executors that drive the `charly check`
beds and return verbatim proof, plus enforcers that gate claims) and dynamic workflows. Whether
you drive `charly` from the keyboard or hand it to an agent, verification uses the same surface.
See [plugins/README.md](plugins/README.md) for the full index.

Charly's MCP surface is available independently: `charly mcp serve` exposes the CLI over Streamable
HTTP or stdio, and container-provided servers auto-discover through `mcp_provide:`.

## License

MIT
