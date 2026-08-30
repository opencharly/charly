# OpenCharly

**The open infrastructure compiler — for you and your agents.**

`charly` is a command-line tool that compiles one declarative description of a working environment
into what each target actually runs. You write a list of **candies** — each one an installable
concern — and `charly` realises the same list on any of five substrates: a container, a VM guest, a
Kubernetes cluster, a host, or an Android device. Every substrate consumes the same intermediate
form, the **install plan**, so moving a deploy from a container to a VM is a keyword change rather
than a rewrite.

The design borrows deliberately from LLVM: one intermediate representation, many backends — applied
to infrastructure deployment instead of code generation, and driven by agents as readily as by
people.

A **candy** is the atomic unit of configuration; a **box** is a candy that composes others into a
buildable machine. The names are literal — `candy:` is a real keyword and `candy/` is a real
directory.

**A box is an artifact; a candybox is a process.** Nothing about a box's contents makes it safe — a
box is deliberately generous, full shell and package manager included. The isolation is a property
of the *running* container or VM — the **candybox**. That is why it is safe to hand an agent
everything inside one, and why `disposable: true` is a statement about a running thing rather than
about a file.

One box can be deployed many times, onto several substrates, on several machines; each of those is
a deploy, and the deploys on one machine are that machine's fleet.

Full documentation: **[opencharly.ai](https://opencharly.ai)**.

---

## Install

**Install `charly` once, then use it from anywhere.** `charly` runs on Linux; container deploys
need Podman or Docker, VM deploys need libvirt. Everything else here — and every page on
[opencharly.ai](https://opencharly.ai) — assumes a machine with `charly` installed.

**[How to install →](https://opencharly.ai/start/install/)**

Once `charly` is on your `$PATH`, `--repo` reads any published project without cloning it:

```bash
charly --repo opencharly/charly box list boxes
```

The first lines of its output:

```
agentteams [testing]
agentteams-manager [testing]
agentteams-worker [testing]
alpine-repo-box [testing]
arch.arch [testing]
…
```

Clone the repository only if you are working *on* charly itself:

```bash
git clone --recurse-submodules https://github.com/opencharly/charly.git
cd charly
task build:binary       # builds ./bin/charly, stamped with a CalVer (date-based) version
./bin/charly box build
```

Every invocation against that checkout uses `./bin/charly`; each checkout or worktree gets its own.

---

## A real box, end to end

This is `box/fedora/box/tutorial-shell/charly.yml`, from the `opencharly/distro-fedora` project:

```yaml
tutorial-shell:
    candy:
        description: |-
            The teaching box behind opencharly.ai's quickstart — a minimal, real dev shell
            ...
        base: fedora
        candy:
            - '@github.com/opencharly/charly/candy/ripgrep:v2026.231.0714'
            - '@github.com/opencharly/charly/candy/sshd:v2026.231.0714'
        plan:
            - check: composing the service candy next to the init candy wired sshd into the assembled supervisord config — a program block neither candy produces on its own
              id: tutorial-shell-service-wired-into-init
              file:
                file: /etc/supervisord.conf
                contains:
                    - contains: "[program:sshd]"
```

`base:` points at another box defined next door; it can equally be a registry ref. The inner
`candy:` is the list of candies the box composes.

Note what is *not* listed: an init system. `sshd` declares a service, so charly resolves whichever
init the *destination* needs and installs it — supervisord when this box is built as a container
image, nothing extra when the same candies land on a systemd machine, because systemd is already
there.

The `plan:` does *not* check that `ripgrep` and `sshd` are present — each candy's own plan proves
that, and those plans run against this same image. It checks what the composition produced: that
`sshd` became a supervisord program. A check belongs on the candy that provides the behaviour, and
belongs on the composing box only when the claim is about the composition itself.

```bash
charly --repo opencharly/distro-fedora box validate                    # the schema gate — nothing runs until it passes
charly --repo opencharly/distro-fedora box build tutorial-shell        # → multi-stage Containerfile → image
charly --repo opencharly/distro-fedora shell tutorial-shell            # → you are inside the candybox
charly --repo opencharly/distro-fedora check run check-tutorial-shell  # → build, deploy, probe, fresh rebuild, tear down
```

### The four stages

| Stage | What you write | What you run |
|---|---|---|
| **Build** | a `candy:` with `base:` and a candy list | `charly box build <box>` |
| **Run** | nothing more | `charly shell <box>` |
| **Deploy** | a substrate keyword — `pod:` `vm:` `kubernetes:` `local:` `android:` | `charly fleet add`, `charly start` |
| **Evaluate** | a `plan:` on each candy | `charly check box`, `charly check live`, `charly check run` |

A deploy marked `disposable: true` is a **check bed**, and `charly check run <bed>` chains build,
deploy and evaluate into one command. It builds the image and checks it, deploys it, and waits for
steady state. Then it checks the running candybox live, destroys and rebuilds it from scratch,
checks it again, and tears everything down.

### Change the substrate, keep the shape

Both stanzas below are real entries in `box/fedora/charly.yml`: this page's example as a container
bed, and its VM twin.

```yaml
# a CONTAINER
check-tutorial-shell:
    pod:
        image: tutorial-shell
        disposable: true

# a VM GUEST
check-fedora-vm:
    vm:
        from: fedora-vm
        disposable: true
        add_candy:
            - '@github.com/opencharly/charly/candy/charly:v2026.231.0714'
```

The payloads differ — the pod runs the built `tutorial-shell` image; the VM boots the `fedora-vm`
template (`from:` inherits its settings) and overlays the `charly` candy. The grammar does not:
the substrate is the keyword, `pod:` versus `vm:`, and each candy a deploy names is realised
through the same install plan on either side — the VM stanza reaches its guest over SSH and
installs packages there, the pod stanza runs an image. `kubernetes:`, `local:` and `android:`
take the same shape —
[how that is wired →](https://opencharly.ai/concepts/02-one-recipe-many-molds/)

### The bigger boxes

The teaching box above has a small candy list. The kitchen-sink dev boxes are `fedora-coder`,
`arch-coder`, `debian-coder` and `ubuntu-coder`. Each carries the AI coding CLIs (`claude-code`,
`codex`, `gemini`, `forgecode`), language runtimes, DevOps tooling, and nested rootless containers
and VMs — all at uid 1000 with no `--privileged`. Same format, same commands. Because those
containers and VMs are nested, a candybox can build and deploy candyboxes — charly runs inside
charly.

---

## How it works

### One keyword, two shapes

A candy is declared with one entity keyword, `candy:`, and one filename, `charly.yml`. But a candy
resolves to **one of two shapes**, and `base:`/`from:` is the switch:

| The candy carries | It is | And it may also carry |
|---|---|---|
| neither `base:` nor `from:` | a **layer** — one installable concern | `package:` `service:` `plan:`, and a `plugin:` block |
| `base:` or `from:` | a **box** — a candy that composes other candies; `box build` turns it into a container image | a `candy:` list of layers, `plan:` |

**The two shapes are mutually exclusive, and the schema enforces it** — a candy that carries both
`base:` and `package:` is rejected. A box does not install packages directly; it composes layers
that do.

### Every word is a plugin

**Every provider word in this document is registered by a plugin candy.** Many of them are
ordinary layers as well — every one carrying a `plan:`, so each is a real layer *and* an extension
of `charly` at the same time. `charly` is not a program with built-in support for containers, VMs
and Kubernetes that also happens to accept plugins. Its core is *word-blind*: it loads plugins and
routes each word to whichever one claims it. It does not know what `pod:` means — there is no `pod`
case in a switch statement in the core.

The [provider index](https://opencharly.ai/reference/providers/) is the live census — every word
and its owning plugin candy, regenerated on every docs build.

| Class | Examples |
|---|---|
| **substrate** | `pod` `vm` `kubernetes` `local` `android` |
| **kind** — the entity keywords themselves | `candy` `distro` `group` `agent` |
| **verb** — probes a `plan:` can call | `file` `http` `cdp` `vnc` `adb` `kube` |
| **command** — `charly` subcommands | `fleet` `check` `clean` `marketplace` |
| **step** — install operations | `service-custom` `reboot` |
| **builder** — multi-stage build patterns | `pixi` `npm` `cargo` `aur` |

**`candy:` itself is a plugin-provided kind**, registered by `candy/plugin-candy-kind`.

**You extend `charly` by writing candies** — in this project or any other, referenced by git URL —
and a substrate you invent is the same kind of thing as `pod:`.

### The architecture, in one pass

**1. Everything starts as one resolved project.** `charly` reads every `charly.yml` it discovers,
follows `@github.com/...` references to other repositories, and resolves each entity to a pinned
CalVer version. The result is a single in-memory project — your candies plus everyone else's,
flattened, with no notion yet of building or deploying. A candy reference is a git URL and a
version, so a box can compose someone else's candy without vendoring it; `charly box reconcile`
keeps the pins aligned.

**2. A plugin is reached the same way wherever it lives.** Some plugin candies are compiled into
the binary; the rest are loaded from a project's `candy/` directory and run as separate processes
over gRPC. Both implement one `Provider` contract, so where a plugin runs is an operational
choice — startup cost against isolation — not an API difference. All five substrates — `pod:`,
`vm:`, `local:`, `kubernetes:`, `android:` — are out-of-process.

**3. Building and deploying share one input.** Building renders a multi-stage Containerfile from
your candy list. Deploying compiles the *same* list into the **install plan**, which each substrate
backend realises its own way: an SSH session against a VM guest, a Kustomize tree, packages on a
host, an APK install — and, on `pod:`, the very image the build path produced.

**4. The artifact carries its own description.** What a box provides and how to prove it — its
declared capabilities and its acceptance plan — are written into the image as `ai.opencharly.*`
labels, so a pulled image can be inspected and tested by a machine that has never seen the source.

**5. The schema is upstream of the code.** The base grammar of `charly.yml` is
[CUE](https://cuelang.org/), a typed configuration language, in the spec module
(`github.com/opencharly/spec`); each plugin ships its own CUE schema for the words it registers. The Go wire types are generated from that base schema, and load-time validation runs
against the same embedded schema — a grammar change cannot reach the code without going through the
module both sides consume.

### Nesting: where a deploy runs

Candies compose a *box*, at authoring time. Nesting places a *deploy*, at run time. A candy is
never "inside" another candy; a deploy can be inside another deploy.

There is no `nested:` field you write. **Nesting is position in the file** — indent one deploy
under another and the inner one runs inside the outer one's candybox:

```yaml
check-group:
    group:
        disposable: true
        ...
    check-group-vm:
        vm:
            from: eval-vm
        check-group-member:
            local:
                from: check-group-app
```

That is a real entry in this repository's `charly.yml`, abridged. The inner `local:` carries no
`host:` field, and that is the point: it runs inside the parent's candybox rather than naming a
machine of its own.

A top-level `local:` deploy installs packages and systemd units onto *the machine charly is
running on*. The same deploy, nested under a disposable `vm:`, installs them into a throwaway
guest instead. Either way it is reversible: a `local:` deploy records each step it applies in an
install ledger, and `charly fleet del <name>` tears it back down.

### Where a running candybox actually lives

**Podman and Docker are both first-class**, for building and for running. `charly` auto-detects
what is installed; either can be pinned per host with `CHARLY_BUILD_ENGINE`, `CHARLY_RUN_ENGINE`
or the runtime config.

On a host with Podman and systemd, a `pod:` deploy runs as user-level systemd quadlets. `charly
config` generates one `charly-<name>.container` quadlet per deploy, carrying its ports, volumes,
devices and security settings; systemd turns it into `charly-<name>.service` and starts it at
boot.

Where Podman and systemd are not both present, the same deploy runs directly against the engine.

---

## Vocabulary

Full teaching glossary: [the words](https://opencharly.ai/concepts/00-vocabulary/).

| Term | What it is |
|---|---|
| **candy** | the atomic unit of configuration in charly — the entity everything else is made of: boxes compose candies, deploys apply them, substrates realise them |
| **layer** | a candy that installs one concern — carrying neither `base:` nor `from:` |
| **base** | a field on a candy that names a starting image — another box or a registry ref. Carrying `base:` makes a candy a **box** |
| **from** | a field with two uses. On a candy, `from: builder:<word>` selects a multi-stage build pattern and makes the candy a **box**. On a deploy, `from: <name>` inherits another same-kind deploy's settings — this sense does not make anything a box |
| **box** | a candy that composes other candies — the composite unit. A box names a starting point via **base** or **from** and stacks layers into a single entity that charly builds into an image or deploys onto a substrate |
| **image** | the built artifact a box produces — the generic word for what `box build` yields, stored in an image store or registry |
| **container image** (= **OCI image**) | an image in the OCI container format — the artifact a `pod:` or `kubernetes:` deploy runs as a container, named after the Open Container Initiative spec that defines it |
| **candybox** | the running, isolated form of a box — a container or a VM guest. **This is the security boundary** |
| **container** | the running OCI process — the candybox's form on a `pod:` deploy. Not the image, not the box |
| **substrate** | the destination kind a deploy lands on — `pod:` `vm:` `kubernetes:` `local:` `android:`. A substrate is a place, not an artifact: the image is the payload, the substrate is where it runs |
| **pod** | the container substrate — a `pod:` deploy runs a box's image as a container. Not a Kubernetes Pod (the `kubernetes:` backend emits real ones) |
| **vm** | the guest substrate — a `vm:` deploy boots a **vm image** as a rootless libvirt guest, reached over SSH |
| **vm image** | the bootable disk a `vm:` deploy boots — a `cloud_image` qcow2 or a bootc image. A *different* artifact from a container image |
| **kubernetes** | the Kubernetes substrate — a `kubernetes:` deploy emits a Kustomize overlay |
| **local** | the host substrate — a `local:` deploy installs onto the machine charly runs on, or onto a remote machine when it carries **host** |
| **android** | the device substrate — an `android:` deploy installs APKs onto a device or emulator |
| **host** | a field on a `local:` deploy naming the machine to install onto — `host: local` (or absent) is the machine charly runs on, `host: <user@machine>` is an SSH target |
| **deploy** | a named placement of a box on a substrate. When running, its candybox is the live thing |
| **fleet** | the set of deploys charly manages on this machine — the boxes deployed together, the way `docker compose` brings up a set of services. `charly fleet add` puts a deploy in it; `charly fleet del` reverses it |
| **plugin** | a candy that teaches charly a new word — it carries a `plugin:` block registering the words it provides, each of which is a **provider**. A plugin lives in the layer shape, but its role is extending charly |
| **provider** | a word a plugin registers, which routes to that plugin when charly sees it — a **kind**, **verb**, **command**, **step**, **builder**, or **substrate** |
| **kind** | the class of a top-level name in a `charly.yml` — the entity keywords |
| **group** | a kind that holds a set of deploys started and torn down together |
| **verb** | a probe a `plan:` step can call — the check vocabulary |
| **command** | a `charly` subcommand — the CLI vocabulary |
| **step** | an install operation in a `plan:` |
| **builder** | a multi-stage build pattern a box can select |
| **plan** | the ordered acceptance spec a candy carries, baked into its image as an OCI label — distinct from the **install plan** |
| **install plan** | the target-neutral form of what a deploy installs — produced once from a box's candy list, then every substrate realises it its own way |
| **check bed** | a deploy marked `disposable: true` — one whose candybox exists to be destroyed and rebuilt, which is what authorises charly to run an unattended full test cycle on it |

---

## Agents drive the same surface

`charly` serves its whole command tree over **MCP** — the Model Context Protocol, the open
standard for exposing tools to an AI agent. It serves on either Streamable HTTP or stdio; an MCP
client sees charly's commands as ordinary tools.

`mcp` is itself an out-of-process command plugin, discovered from a project's `candy/plugin-mcp`
rather than compiled into the binary — so point charly at a project that supplies it:

```bash
charly --repo opencharly/charly mcp serve
```

[AGENTS.md](AGENTS.md) is the complete, harness-neutral rulebook; `CLAUDE.md` is one adapter of
it, not a separate source of truth. Supporting a new harness means adding an adapter, never
porting the project.

[opencharly/marketplace](https://github.com/opencharly/marketplace) ships one **skill** — a
packaged instruction set an agent loads — for every candy, box, command and contributor
subsystem. It also ships reusable agents: executors that drive the `charly check` beds and return
verbatim proof; enforcers that gate claims. It installs through the plugin manager of each
supported harness — Claude Code, Cursor, Codex CLI, Kimi Code and `pi`. The exact commands are in
that repository's README, which doubles as the full skill index.

---

## Documentation

Everything factual about a candy, box, plugin or verb is **generated** from the sources in this
repository and published at [opencharly.ai](https://opencharly.ai), so it cannot drift from the
code the way a hand-maintained copy in this file would.

| You want | Go to |
|---|---|
| to build your first thing | [Quickstart](https://opencharly.ai/start/quickstart/) → [Authoring a candy](https://opencharly.ai/guides/authoring-a-candy/) |
| the vocabulary | [The words](https://opencharly.ai/concepts/00-vocabulary/) |
| the ideas, in order, with runnable examples | [The concepts tour](https://opencharly.ai/concepts/01-the-box-is-the-boundary/) — twelve short pages |
| every command and flag | [CLI reference](https://opencharly.ai/reference/cli/fleet/) + [The charly CLI](https://opencharly.ai/guides/the-cli/) |
| every candy and box | [Candy reference](https://opencharly.ai/reference/candy/github.com/opencharly/pod-sshd:v2026.239.1637/sshd/) · [Box reference](https://opencharly.ai/reference/box/fedora/tutorial-shell/) |
| "what implements `cdp:`?" | [Provider index](https://opencharly.ai/reference/providers/) |
| something is broken | [Troubleshooting](https://opencharly.ai/guides/troubleshooting/) |
| why the project looks like this | [The vision](https://opencharly.ai/vision/) · [What it is reacting to](https://opencharly.ai/grievances/) |
| dated history | each repo's [`CHANGELOG/`](CHANGELOG/README.md), one file per CalVer version |

## License

MIT
