# OpenCharly

**The fully stocked gourmet kitchen for you and your agents.**

`charly` is a command-line tool that builds container images from a declarative list of **candies**, and applies
that same list to a VM guest, a Kubernetes cluster, a host, or an Android device. Every substrate
compiles to one shared install plan, so moving a deploy from a container to a VM is a keyword
change rather than a rewrite.

Every candy also carries an acceptance plan, baked into the built image as an
`ai.opencharly.description` label in the **OCI** (Open Container Initiative) metadata every
container image carries. The image can therefore be tested later, on a machine that
has never seen this repository.

Full documentation, including a quickstart and a generated reference for every candy, box, plugin
and verb: **[opencharly.ai](https://opencharly.ai)**.

---

## How it works

### The words

The vocabulary is defined once, here — this is the reference the whole project uses, and the rest
of this page and every skill and doc page follows it. Full teaching glossary:
[the words](https://opencharly.ai/concepts/00-vocabulary/).

| Term | What it is |
|---|---|
| **candy** | the atomic unit of configuration in charly — the entity everything else is made of: boxes compose candies, deploys apply them, substrates realise them |
| **layer** | a candy that installs one concern — carrying neither `base:` nor `from:` |
| **base** | a field on a candy that names a starting image — another box or a registry ref. Carrying `base:` makes a candy a **box** |
| **from** | a field with two uses: on a box, a **builder** reference (`builder:<word>`) for a multi-stage build; on a deploy, inherits a same-kind template. Carrying `from:` makes a candy a **box** |
| **box** | a candy that composes other candies — the composite unit. A box names a starting point via **base** or **from** and stacks layers into a single entity that charly builds into an image or deploys onto a substrate |
| **image** | the built artifact a box produces — the generic word for what `box build` yields, stored in an image store or registry |
| **container image** | an image in the OCI container format — the artifact a `pod:` or `k8s:` deploy runs as a container. The same thing as an **OCI image** |
| **OCI image** | a container image, named after the Open Container Initiative format that defines it — synonymous with **container image** |
| **candybox** | the running, isolated form of a box — a container, a VM guest, or a check bed. **This is the security boundary** |
| **container** | the running OCI process — the candybox's form on a `pod:` deploy. Not the image, not the box |
| **substrate** | the destination kind a deploy lands on — `pod:` `vm:` `k8s:` `local:` `android:`. A substrate is a place, not an artifact: the image is the payload, the substrate is where it runs |
| **pod** | the container substrate — a `pod:` deploy runs a box's image as a container. Not a Kubernetes Pod (the `k8s:` backend emits real ones) |
| **vm** | the guest substrate — a `vm:` deploy boots a **vm image** as a rootless libvirt guest, reached over SSH |
| **vm image** | the bootable disk a `vm:` deploy boots — a `cloud_image` qcow2 or a bootc image. A *different* artifact from a container image |
| **k8s** | the Kubernetes substrate — a `k8s:` deploy emits a Kustomize overlay |
| **local** | the host substrate — a `local:` deploy installs onto the machine charly runs on, or onto a remote machine when it carries **host** |
| **android** | the device substrate — an `android:` deploy installs APKs onto a device or emulator |
| **host** | a field on a `local:` deploy naming the machine to install onto — `host: local` (or absent) is the machine charly runs on, `host: <user@machine>` is an SSH target |
| **deploy** | a named placement of a box on a substrate, written as `pod:` `vm:` `k8s:` `local:` `android:`. When running, its candybox is the live thing |
| **fleet** | the set of deploys charly manages on this machine — the boxes deployed together, the way `docker compose` brings up a set of services. `charly fleet add` puts a deploy in it; `charly fleet del` reverses it |
| **plugin** | a candy that teaches charly a new word — it carries a `plugin:` block registering the words it provides, each of which is a **provider**. A plugin lives in the layer shape, but its role is extending charly, not installing a concern |
| **provider** | a word a plugin registers, which routes to that plugin when charly sees it — a **kind**, **verb**, **command**, **step**, **builder**, or **substrate** |
| **kind** | the class of a top-level name in a `charly.yml` — the entity keywords (`candy`, `distro`, `group`, `builder`, `agent`) |
| **verb** | a probe a `plan:` step can call — the check vocabulary (`file`, `http`, `cdp`, `vnc`, `adb`, `kube`) |
| **command** | a `charly` subcommand — the CLI vocabulary (`fleet`, `check`, `candy`, `clean`) |
| **step** | an install operation in a `plan:` — (`file`, `service-custom`, `reboot`) |
| **builder** | a multi-stage build pattern a box can select — (`pixi`, `npm`, `cargo`, `aur`) |
| **plan** | the ordered acceptance spec a candy carries, baked into its image as an OCI label — distinct from the **install plan** |
| **install plan** | the target-neutral form of what a deploy installs — produced once from a box's candy list, then every substrate realises it its own way. It is why `pod:` → `vm:` is a keyword change, not a rewrite |
| **IR** | intermediate representation — the compiler term for the **install plan**: the neutral form in the middle of a compile, written once and lowered to each target |
| **check bed** | a deploy marked `disposable: true` — a candybox that exists to be destroyed and rebuilt, which is what authorises charly to run an unattended full test cycle on it |

Two pairs are worth separating deliberately, because conflating them is how the security story and
the deploy model get misread.

**A box is an artifact; a candybox is a process.** Nothing about the image's contents makes it safe
— a box is deliberately generous, full shell and package manager included. The isolation is a
property of the *running* container or VM, which is also why it is safe to hand an agent everything
inside one, and why `disposable: true` is a statement about a running thing rather than about a file.

**Box vs image — authored vs built.** A box is the authored recipe; `charly box build` turns it
into a container image. The schema's `#Image` def is the box arm — that is a code name, not a
license to call a box an image. In prose: a box builds into a container image (or 'renders to').
A container image is the built artifact; a vm image is the bootable disk a VM boots; the two are
not the same artifact, and neither is a box.

**Box vs fleet — an artifact versus a set of placements.** A box is something you build. A fleet is
the set of places you have put it on this machine. One box can be deployed many times, onto several
substrates, on several machines; each of those is a deploy in some fleet. The two never collide in
prose or in `charly --help`: box is what you build, fleet is the set of deploys charly manages here.

### One keyword, two shapes

Here is the idea that causes the most confusion, so it is worth being exact — and exactness here
means saying what the schema actually enforces, not what would be tidy.

There is one entity keyword, `candy:`, and one filename, `charly.yml`. But a candy resolves to
**one of two shapes**, and `base:`/`from:` is the switch:

| The candy carries | It is | And it may also carry |
|---|---|---|
| neither `base:` nor `from:` | a **layer** — one installable concern | `package:` `service:` `plan:`, and a `plugin:` block |
| `base:` or `from:` | a **box** — a candy that composes other candies; `box build` turns it into a container image | a `candy:` list of layers, `plan:` |

**The two shapes are mutually exclusive, and the schema enforces it.** `spec/schema/node.cue`
defines `#CandyValue: (*#Candy | #Image)` — a closed two-arm disjunction. Add `base:` to a candy
that declares `package:` and `charly box validate` rejects it: *`base: field not allowed` /
`package: field not allowed`*. A box does not install packages directly; it composes layers that
do.

**The one thing that genuinely is additive is `plugin:`.** A layer may also register providers, and
in this repository 86 candies do — every one of them carrying a `plan:`, so each is a real layer
*and* an extension of `charly` at the same time. That is the additive case, and it lives entirely
inside the layer shape.

So the accurate model is: **a candy is a layer or a box, never both — and a layer can additionally
be a plugin.**

### And the vocabulary itself is open

That third row is bigger than it looks, and it is the part people miss.

**Every keyword in this document is registered by a plugin candy.** `charly` is not a program with
built-in support for containers, VMs and Kubernetes that also happens to accept plugins. Its core
is *kind-blind*: it knows how to load plugins, route a word to whichever one claims it, and carry
generic data between them. It does not know what `pod:` means.

Today's catalog registers **123 words across 73 plugin candies**:

| Class | How many | Examples |
|---|---|---|
| **deploy** substrates | 5 | `pod` `vm` `k8s` `local` `android` |
| **kind** — the entity keywords themselves | 14 | `candy` `distro` `group` `builder` `agent` |
| **verb** — probes a `plan:` can call | 35 | `file` `http` `cdp` `vnc` `adb` `kube` |
| **command** — `charly` subcommands | 45 | `fleet` `check` `candy` `clean` |
| **step** — install operations | 12 | `file` `service-custom` `reboot` |
| **builder** — multi-stage build patterns | 4 | `pixi` `npm` `cargo` `aur` |
| the build/load internals | 8 | `build:box` `loader:loader` `refs:refs` `terminal:tmux` |

A further **14 words across 13 `plugin-example-*` candies** are test fixtures — they exist to
exercise the plugin mechanisms themselves, and are excluded from the table. That exclusion is the
difference between a deploy row reading 5 and reading 7: `exampledeploy` and `examplelifecycle`
are not substrates you can put anything on. **73 real + 13 fixtures = the 86 plugin candies
counted elsewhere on this page** — the two numbers are the same set, split rather than in tension.

Read the **kind** row again: **`candy:` itself is a plugin-provided kind**, registered by
`candy/plugin-candy-kind`. The keyword this entire page is about is not privileged — it is a word
some candy claimed.

So there is no fixed list of substrates, no fixed list of check verbs, no built-in set to petition
for additions to. A plugin lives either compiled into the binary or loaded from a project's own
`candy/` directory — including a project that is not this one, referenced by git URL. **You extend
`charly` by writing candies, as many as you like**, and a substrate you invent is the same kind of
thing as `pod:`.

### The architecture, in one pass

It is worth seeing how those pieces actually fit, because the shape explains what you can do with
them.

**1. Everything starts as one resolved project.** `charly` reads every `charly.yml` it discovers,
follows `@github.com/...` references to other repositories, and resolves each entity to a pinned
CalVer version. The result is a single in-memory project — your candies plus everyone else's,
flattened, with no notion yet of building or deploying.

**2. The core does not know what anything means.** What survives in `charly` itself is deliberately
tiny: load plugins, look at a word and route it to whichever plugin claimed it, broker messages
between them, and carry *generic envelopes* — data whose shape the core never inspects. There is no
`pod` case in a switch statement anywhere in it. A test (`charly/import_purity_test.go`) fails the
build if core code reaches for anything richer.

**3. A plugin is reached the same way wherever it lives.** Of the 86 plugin candies, **58 are
compiled into the binary** and the rest run as separate processes over gRPC. Both implement one
`Provider` contract, so placement is an operational choice — startup cost against isolation — not
an API difference. `deploy:pod`, `deploy:vm` and `deploy:local` are all out-of-process: the
substrates people think of as "built in" are not even in the binary.

**4. Two paths diverge, then one rejoins.** Building renders a multi-stage Containerfile from your
candy list. Deploying compiles the *same* list into an **install plan** — the shared intermediate
representation — which each substrate backend then realises its own way: a container image, an SSH
session against a VM guest, a Kustomize tree, packages on a host, an APK install. This is the
mechanism behind "swap the substrate, not the recipe". It is one data structure, and everything
downstream consumes it.

**5. The artifact carries its own description.** Capabilities and the acceptance plan are written
into the image as `ai.opencharly.*` labels, so a pulled image can be inspected and tested by a
machine that has never seen the source. The image is not an opaque blob with documentation
elsewhere; the documentation is inside it.

**6. The schema is upstream of all of it.** The `charly.yml` grammar is defined once in CUE. The Go
types, the load-time validation, and the reference pages on opencharly.ai are all generated from
that one definition — so a schema change cannot reach the code without reaching the docs.

### What the shape makes possible

Each of these is a consequence of the layout above rather than a separate feature:

- **Invent a substrate.** Deploy targets are plugins with no privileged status. Something charly has
  never heard of — a NAS, a router, a robot — is a plugin candy that consumes the install plan.
- **Compose across organisations.** A candy reference is a git URL and a version. Your box can
  compose someone else's candy without vendoring it, and `charly box reconcile` keeps the pins
  aligned.
- **Run charly inside charly.** The dev boxes contain rootless containers and rootless VMs, so a
  candybox can build and deploy candyboxes. Verification becomes self-hosting rather than requiring
  a privileged outer host.
- **Hand the whole thing to an agent.** Because there is no automation-only path and every capability
  is a plugin, an agent gets the same surface you do — including the ability to extend it.
- **Prove it, disposably.** Any deploy marked `disposable: true` can be destroyed and rebuilt without
  asking, which is what makes "test it for real" affordable enough to do on every change.

That is the whole reason the core stays small while the catalog grows, and why there is no second
vocabulary: extending the tool and using the tool are the same activity.

### The four stages

| Stage | What you write | What you run |
|---|---|---|
| **Build** | a `candy:` with `base:` and a candy list | `charly box build <box>` |
| **Run** | nothing more | `charly shell <box>` |
| **Deploy** | a substrate keyword — `pod:` `vm:` `k8s:` `local:` `android:` | `charly fleet add`, `charly start` |
| **Evaluate** | a `plan:` on each candy | `charly check box`, `charly check live`, `charly check run` |

A **check bed** chains build, deploy and evaluate into one command — every stage above except
**Run**, which is the interactive one. `charly check run <bed>` builds the image, checks it,
deploys it, waits for steady state, checks it live, then destroys and rebuilds it from scratch and
checks it again, then tears everything down.

### Nesting: where a deploy runs

The second thing worth being exact about, and it is a **different** idea from the one above.
Capabilities compose a *candy*, at authoring time. Nesting places a *deploy*, at run time. A candy
is never "inside" another candy; a deploy can be inside another deploy.

There is no `nested:` field. **Nesting is position in the file** — indent one deploy under another
and the inner one runs inside the outer one's venue:

```yaml
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

That is a real entry in this repository's `charly.yml`, abridged. The inner `local:` carries no
`host:` field, and that is the point: it inherits the parent's venue rather than naming one.

**Why this matters more than it looks.** A top-level `local:` deploy installs packages and systemd
units onto *the machine charly is running on*. The same four lines, nested under a disposable
`vm:`, install them into a throwaway guest instead. The authoring shape does not change — only its
position in the tree does, and that position is the whole difference between editing your
workstation and editing something you can destroy.

**Nesting is not the same as membership.** A deploy indented under another *runs inside* it. A
deploy listed as a sibling member *runs beside* it — a companion, reachable at `${HOST:<member>}`,
sharing a lifecycle but not a machine. Children go in; siblings go next to.

### Where a running candybox actually lives

**Podman and Docker are both first-class**, for building and for running, and the two are chosen
independently: `charly` auto-detects what is installed and either can be pinned per host
(`CHARLY_BUILD_ENGINE`, `CHARLY_RUN_ENGINE`, or the runtime config). The mixed pair — build with
Podman, run under Docker — is asserted by `TestResolveRuntime_MixedEngines`, which fails if
resolution ever collapses the two onto one engine.

On a host with Podman and systemd, a `pod:` deploy is realised as **user-level systemd quadlets**.
`charly config` generates one `charly-<name>.service` per deploy, carrying its ports, volumes,
devices and security settings, and systemd starts it at boot. A deploy whose volumes are encrypted
with a keyring backend (Secret Service) starts at boot too and then suspends until the key is
available; one whose passphrase sits in a plaintext config file does not autostart — it waits for
an explicit `charly start`.

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
task build:binary       # builds ./bin/charly, stamped with a CalVer (date-based) version
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
entries in `box/fedora/charly.yml`, cut down to the lines that carry the point — the
`description:`, `lifecycle:` and `plan:` of both, plus `check-fedora-vm`'s `install_opts:`.
That last one matters if you copy this: the real VM entry sets `allow_repo_changes`,
`allow_root_tasks` and `skip_incompatible`, and a guest install needs those permissions. Read the
entry, not this excerpt, before adapting it.

```yaml
# a CONTAINER
check-tutorial-shell:
    pod:
        image: tutorial-shell
        disposable: true

# a VM GUEST — same candy-reference form
check-fedora-vm:
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

**Charly is not built for one agent harness.** There is no agent-only interface and no automation-only
verb: the CLI *is* the surface, so anything that can run a command can drive every stage above. On
top of that, `charly` serves its whole command tree over **MCP** — the Model Context Protocol, the
open standard for exposing tools to an AI agent — over either Streamable HTTP or stdio, so an MCP
client sees charly's verbs as ordinary tools. Container-provided servers auto-discover through
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
| every command, flag and verb | [CLI reference](https://opencharly.ai/reference/cli/fleet/) + [The charly CLI](https://opencharly.ai/guides/the-cli/) |
| every candy and box | [Candy reference](https://opencharly.ai/reference/candy/ripgrep/) · [Box reference](https://opencharly.ai/reference/box/fedora/tutorial-shell/) |
| "what implements `cdp:`?" | [Provider index](https://opencharly.ai/reference/providers/) |
| something is broken | [Troubleshooting](https://opencharly.ai/guides/troubleshooting/) |
| why the project looks like this | [The vision](https://opencharly.ai/vision/) · [What it is reacting to](https://opencharly.ai/grievances/) |
| dated history | each repo's [`CHANGELOG/`](CHANGELOG/README.md), one file per CalVer version |

## License

MIT
