# OpenCharly

**The fully stocked gourmet kitchen for you and your agents.**

`charly` is a CLI that builds container images from a declarative list of **candies**, and applies
that same list to a VM guest, a Kubernetes cluster, a host, or an Android device. Every substrate
compiles to one shared install plan, so moving a deploy from a container to a VM is a keyword
change rather than a rewrite.

Every candy also carries an acceptance plan, baked into the built image as an
`ai.opencharly.description` OCI label. The image can therefore be tested later, on a machine that
has never seen this repository.

Full documentation, including a quickstart and a generated reference for every candy, box, plugin
and verb: **[opencharly.ai](https://opencharly.ai)**.

---

## How it works

### The words

Seven terms, used precisely throughout. Full glossary:
[the words](https://opencharly.ai/concepts/00-vocabulary/).

| Term | What it is |
|---|---|
| **candy** | one entry in a `charly.yml`. The **only** entity kind there is — everything below is a candy, or a thing a candy produces |
| **box** | a candy carrying `base:`/`from:`, so it builds into a **container image** |
| **candybox** | a box in its **running, isolated form** — container, VM, or check bed |
| **deploy** | a named placement of a box on a substrate, written as `pod:` `vm:` `k8s:` `local:` `android:` |
| **bundle** | the set of deploys charly manages on this machine. `charly bundle add` puts a deploy in it |
| **plan** | the ordered acceptance spec a candy carries, baked into its image as an OCI label |
| **check bed** | a deploy marked `disposable: true`, which is what authorises charly to destroy and rebuild it unattended |

Two of those are worth separating deliberately, because conflating them is how the security story
gets misread. **A box is an artifact; a candybox is a process.** Nothing about the image's contents
makes it safe — a box is deliberately generous, full shell and package manager included. The
isolation is a property of the *running* container or VM, which is also why it is safe to hand an
agent everything inside one, and why `disposable: true` is a statement about a running thing rather
than about a file.

### One keyword, three roles

There is one entity keyword, `candy:`, and one filename, `charly.yml`. What you put inside the
keyword decides what the entity is:

| What it declares | The role it plays | How it is used |
|---|---|---|
| `package:` / `plan:` / `service:` | a **layer** — one concern | listed in any box's `candy:` list |
| …plus `base:` or `from:` | a **box** — a buildable image | `charly box build <name>` |
| …plus a `plugin:` block | a **plugin** — a new verb, kind or command for `charly` itself | loaded on demand, or compiled in |

"Layer" and "box" are *roles*, not separate entity kinds — the same `candy:` keyword, distinguished
only by what is inside it. Every capability beyond the core is that third row: verbs, deploy kinds,
probes, builders and whole command trees are all plugin candies.

### The four stages

| Stage | What you write | What you run |
|---|---|---|
| **Build** | a `candy:` with `base:` and a candy list | `charly box build <box>` |
| **Run** | nothing more | `charly shell <box>` |
| **Deploy** | a substrate keyword — `pod:` `vm:` `k8s:` `local:` `android:` | `charly bundle add`, `charly start` |
| **Evaluate** | a `plan:` on each candy | `charly check box`, `charly check live`, `charly check run` |

A **check bed** chains build, deploy and evaluate into one command — every stage above except
**Run**, which is the interactive one. `charly check run <bed>` builds the image, checks it,
deploys it, waits for steady state, checks it live, then destroys and rebuilds it from scratch and
checks it again, then tears everything down.

### Where a running candybox actually lives

**Podman and Docker are both first-class**, for building and for running, and the two are chosen
independently: `charly` auto-detects what is installed and either can be pinned per host
(`CHARLY_BUILD_ENGINE`, `CHARLY_RUN_ENGINE`, or the runtime config). The mixed pair — build with
Podman, run under Docker — is asserted by `TestResolveRuntime_MixedEngines`, which fails if
resolution ever collapses the two onto one engine.

On a host with Podman and systemd, a `pod:` deploy is realised as **user-level systemd quadlets**.
`charly config` generates one `charly-<name>.service` per deploy, carrying its ports, volumes,
devices and security settings, and systemd starts it at boot. A deploy with encrypted volumes
starts at boot too, then suspends until its encryption key is available.

Where Podman and systemd are not both present, the same deploy runs directly against the engine
instead. You do not choose between the two: the deploy is described once, and `charly` resolves
which mode the host supports.

---

## Install

**Install `charly` as a native package.** That is how you use it, and everything else here — and
every page on [opencharly.ai](https://opencharly.ai) — assumes a machine with `charly` installed
and no charly checkout anywhere. Build the package once from a source tree, then install it with
your own package manager. Building the package needs Go 1.26+ and
[go-task](https://taskfile.dev); `pkg:fedora` and `pkg:debian` build distro-natively in a
container, so they also need **podman**, while `pkg:arch` runs `makepkg` directly and therefore
needs an **Arch-family host**:

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

---

## A real box, end to end

This is `box/fedora/box/tutorial-shell/charly.yml`, re-proven on every acceptance run by the
`check-tutorial-shell` bed:

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

`base:` points at another box defined next door; it can equally be a registry ref. `ripgrep` is a
**tool** layer (packages and probes, no service); `sshd` is a **service** layer.

Note what is *not* listed: an init system. `sshd` declares a service, so charly resolves whichever
init the *destination* needs and installs it — supervisord when this box is built as a container
image, nothing extra when the same candies land on a systemd machine, because systemd is already
there. You declare the service; the init follows from where it ends up.

The `plan:` does *not* check that `rg` and `sshd` are present — each candy's own plan proves that,
and those plans run against this same image. It checks what **composition** produced: that `sshd`
became a supervisord program. A check belongs on the behaviour's provider, and belongs on the
composing box only when the claim is about the composition itself.

```bash
charly --repo opencharly/distro-fedora box validate                    # the schema gate — nothing runs until it passes
charly --repo opencharly/distro-fedora box build tutorial-shell        # → multi-stage Containerfile → image
charly --repo opencharly/distro-fedora shell tutorial-shell            # → you are inside the candybox
charly --repo opencharly/distro-fedora check run check-tutorial-shell  # → build, deploy, probe, fresh rebuild, tear down
```

### Change the mold, keep the recipe

That is the claim, so here is the evidence rather than the assertion. Both stanzas below are real
entries in `box/fedora/charly.yml`, abridged only by cutting their prose descriptions.

```yaml
check-tutorial-shell:                # a CONTAINER
    pod:
        image: tutorial-shell
        disposable: true

check-fedora-vm:                     # a VM GUEST — same candy-reference form
    vm:
        from: fedora-vm
        disposable: true
        add_candy:
            - '@github.com/opencharly/charly/candy/charly:v2026.201.0706'
```

The substrate is the keyword — `pod:` versus `vm:` — and the candies are named the same way on
both sides. The VM stanza reaches its guest over SSH and installs packages there; the pod stanza
builds an image. Neither the candy list nor its `@github…` pin format changes. `k8s:`, `local:` and
`android:` take the same shape.

**`local:` deserves one warning**, because it is the substrate that touches a real machine:
it installs packages and systemd units onto whatever host it targets. Point it at a disposable VM
guest before you point it at your workstation.
[How that is wired →](https://opencharly.ai/concepts/02-one-recipe-many-molds/)

**The other end of the scale.** The box above has two candies. The **kitchen-sink dev boxes** —
`fedora-coder` and its `arch`, `debian` and `ubuntu` siblings — carry around thirty each: four AI
coding CLIs (`claude-code`, `codex`, `gemini`, `forgecode`), every language runtime, DevOps
tooling, nested rootless containers and rootless libvirt VMs, all at uid 1000 with no
`--privileged`. Same format, same commands. A fully stocked kitchen really does ship with the sink.

---

## Agents drive the same surface

**Charly is not built for one agent harness.** There is no agent-only API and no automation-only
verb: the CLI *is* the surface, so anything that can run a command can drive every stage above. On
top of that, `charly` serves its whole command tree over **MCP** (Streamable HTTP or stdio), so an
MCP client sees charly's verbs as ordinary tools. Container-provided servers auto-discover through
`mcp_provide:`.

`mcp` is itself an out-of-process command plugin, discovered from a project's `candy/plugin-mcp`
rather than compiled into the binary — so point charly at a project that supplies it:

```bash
charly --repo opencharly/charly mcp serve
```

The project's rules are written the same way. [AGENTS.md](AGENTS.md) is the complete,
**harness-neutral** rulebook and is sufficient on its own; `CLAUDE.md` is one adapter of it, not a
separate source of truth. Supporting a new harness means adding an adapter, never porting the
project.

[opencharly/plugins](https://github.com/opencharly/plugins) is one skill tree, in which every candy,
box, command and contributor subsystem has an owning skill, plus reusable plugin agents (executors
that drive the `charly check` beds and return verbatim proof, and enforcers that gate claims) and
dynamic workflows. It installs into whichever harnesses have an adapter today — currently Claude
Code, Codex and Kimi — in three modes: `developer` (every plugin), `user` (use and author with
charly, without contributor internals), and `container <family>` (one generated container family).
It writes only target-repository files, never any user configuration, and does not depend on MCP.
See [plugins/README.md](plugins/README.md) for the full index.

Setup instructions live in that repository's own README. They currently require a checkout, which
is a gap rather than a design: there is no way to install the skill tree with only the `charly`
binary, including for the `user` mode aimed at people who are explicitly not developing charly.
Tracked as [#210](https://github.com/opencharly/charly/issues/210).

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
| every command, flag and verb | [CLI reference](https://opencharly.ai/reference/cli/bundle/) + [The charly CLI](https://opencharly.ai/guides/the-cli/) |
| every candy and box | [Candy reference](https://opencharly.ai/reference/candy/ripgrep/) · [Box reference](https://opencharly.ai/reference/box/fedora/tutorial-shell/) |
| "what implements `cdp:`?" | [Provider index](https://opencharly.ai/reference/providers/) |
| something is broken | [Troubleshooting](https://opencharly.ai/guides/troubleshooting/) |
| why the project looks like this | [The vision](https://opencharly.ai/vision/) · [What it is reacting to](https://opencharly.ai/grievances/) |
| dated history | each repo's [`CHANGELOG/`](CHANGELOG/README.md), one file per CalVer version |

## License

MIT
