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

Six terms, used precisely throughout. `box` and `candybox` are distinct, and are the pair most
often used interchangeably. Full glossary:
[the words](https://opencharly.ai/concepts/00-vocabulary/).

| Term | What it is | What it is *not* |
|---|---|---|
| **candy** | one entry in a `charly.yml` — the **only** entity kind there is | not "a layer", not "a package" |
| **box** | a candy carrying `base:`/`from:` → a buildable **container image** | not the running thing |
| **candybox** | a box in its **running, isolated form** — container, VM, or check bed. **The security boundary.** | not the image, not the config |
| **check bed** | a deploy marked `disposable: true` — a candybox that exists to be destroyed | not a test file |
| **plan** | the ordered acceptance spec a candy carries, baked into the image as an OCI label | not a build script |
| **substrate** | where a deploy lands: `pod:` `vm:` `k8s:` `local:` `android:` | |

### One keyword, three roles

There is one entity keyword, `candy:`, and one filename, `charly.yml`. What you put inside the
keyword decides what the entity is:

| What it declares | What it is | How it is used |
|---|---|---|
| `package:` / `plan:` / `service:` | a **layer** — one concern | spliced into any box's `candy:` list |
| …plus `base:` or `from:` | a **box** — a buildable image | `charly box build <name>` |
| …plus a `plugin:` block | a **plugin** — a new verb, kind or command for `charly` itself | loaded on demand, or compiled in |

Every capability beyond the core is that third row. Verbs, deploy kinds, probes, builders and
whole command trees are all plugin candies, which is why the core stays small while the catalog
grows, and why there is no second vocabulary to learn.

### The four stages

| Stage | What you write | What you run |
|---|---|---|
| **Build** | a `candy:` with `base:` and a candy list | `charly box build <box>` |
| **Run** | nothing more | `charly shell <box>` |
| **Deploy** | a substrate keyword — `pod:` `vm:` `k8s:` `local:` `android:` | `charly bundle add`, `charly start` |
| **Evaluate** | a `plan:` on each candy | `charly check box`, `charly check live`, `charly check run` |

A **check bed** — a deploy marked `disposable: true` — runs all four in one command. `charly check
run <bed>` builds the image, checks it, deploys it, waits for steady state, checks it live, then
destroys and rebuilds from scratch and checks it again, then tears everything down.

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

Note what is *not* listed: an init system. `sshd` declares a service, so charly resolves the init
this target needs and installs it — supervisord for a container, nothing extra for a systemd
machine, because systemd is already there. You declare the service; the init follows.

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

**Then change the mold and keep the recipe.** The same candy list applies to a VM guest over SSH, a
Kubernetes cluster, or a host — swap the substrate in the deploy, not the recipe.

The host substrate (`local:`) is the one to try *inside a candybox first*: it installs packages and
units onto whatever machine it targets, so point it at a disposable VM guest rather than your
workstation. [How that is wired →](https://opencharly.ai/concepts/02-one-recipe-many-molds/)

**The other end of the scale.** The box above has two candies. The **kitchen-sink dev boxes** —
`fedora-coder` and its `arch`, `debian` and `ubuntu` siblings — carry around thirty each: four AI
coding CLIs (`claude-code`, `codex`, `gemini`, `forgecode`), every language runtime, DevOps
tooling, nested rootless containers and rootless libvirt VMs, all at uid 1000 with no
`--privileged`. Same format, same commands. A fully stocked kitchen really does ship with the sink.

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

## Agents drive the same surface

Charly's MCP surface exposes the CLI over Streamable HTTP or stdio, and container-provided servers
auto-discover through `mcp_provide:`. `mcp` is itself an out-of-process command plugin, discovered
from a project's `candy/plugin-mcp` rather than compiled into the binary — so point charly at a
project that supplies it:

```bash
charly --repo opencharly/charly mcp serve
```

[opencharly/plugins](https://github.com/opencharly/plugins) is one skill tree for Claude Code,
Codex, and Kimi, teaching each harness how to compose, build, deploy, check, and manage boxes.
Every candy, box, command, and contributor subsystem has an owning skill. It installs in three
modes — `developer` (every plugin), `user` (use and author with charly, without contributor
internals), and `container <family>` (one generated container family) — and writes only
target-repository files, never `~/.claude`, `~/.codex`, `~/.kimi-code`, or any other user
configuration. It does not depend on MCP.

Setup instructions live in that repository's own README. They currently require a checkout, which
is a gap rather than a design: there is no way to install the skill tree with only the `charly`
binary, including for the `user` mode aimed at people who are explicitly not developing charly.
Tracked as [#210](https://github.com/opencharly/charly/issues/210).

Beyond skills, the project ships reusable plugin agents (executors that drive the `charly check`
beds and return verbatim proof, plus enforcers that gate claims) and dynamic workflows. Whether you
drive `charly` from the keyboard or hand it to an agent, verification uses the same surface. See
[plugins/README.md](plugins/README.md) for the full index.

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
