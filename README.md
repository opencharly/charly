# OpenCharly

**The candy factory for you and your agents.**

Describe what you need in a simple candy list, and `charly` composes it
into optimized multi-stage **boxes** (container images) — from an
interactive dev shell to a running service to a systemd unit to a
bootable VM, to an agent's desktop running inside a candybox. Works the
same way whether you're at the keyboard or your agents are
driving.

187 candies across this repo and its submodules. 53 box definitions
(39 enabled by default). 2 VM definitions, 2 Android devices, and a
growing catalog of `kind: local` host templates and disposable
check beds. Docker and Podman. `linux/amd64`. Fedora, Debian, Ubuntu,
Arch, and CachyOS. One CLI: `charly` (29 top-level verbs). Every candy,
box, VM, and command has a dedicated recipe card (skill) — ~290 skills
across 25 plugins. See `plugins/README.md` for the full index.


> **New here?** [VISION.md](VISION.md) is the one-page thesis — why OpenCharly
> secures the box and fills it with the whole candy store, and where the
> factory is heading.

## Table of contents

- [What's in the chocolate factory](#whats-in-the-chocolate-factory)
- [Core concepts](#core-concepts)
- [Why OpenCharly?](#why-opencharly)
- [Install](#install)
- [Quickstart](#quickstart)
- [Lifecycle](#lifecycle)
  - [Build](#build)
  - [Run](#run)
  - [Deploy](#deploy)
  - [Evaluate](#evaluate)
  - [Author with agents](#author-with-agents)
  - [Manage](#manage)
- [Command reference](#command-reference)
- [Catalogs](#catalogs)
- [Troubleshooting](#troubleshooting)
- [Adding a candy](#adding-a-candy)
- [Works with Claude Code](#works-with-claude-code)
- [License](#license)

## What's in the chocolate factory

`charly` is a Swiss Chocolate Factory. Each production line is a stage of
the lifecycle — **build, run, deploy, evaluate** — driven
from one config and one mental model:

| Reach for `charly` when you want to…                            | …and you get                                       | Stage                 |
|-------------------------------------------------------------|----------------------------------------------------|-----------------------|
| compose a reproducible box from a candy list                | a `candy:` with `base:`, `charly box build`        | [Build](#build)       |
| run one or more containers as a managed pod                 | `kind: pod`, `charly bundle add`, `charly start`           | [Run](#run)           |
| apply the same candies to a host, VM, k8s, or Android device | `charly bundle add` + a substrate kind                 | [Deploy](#deploy)     |
| prove a config actually works, end-to-end                   | a disposable check bed, `charly check run`, baked `check:` checks  | [Evaluate](#evaluate) |

The same `charly` drives two further stages — it
[authors candies and boxes with an agent in the loop](#author-with-agents)
and [manages](#manage) the running lifecycle (cleanup, diagnostics,
schema upgrades, runtime config).

> One `charly.yml`, one box, one per-host `charly.yml` overlay, and one disposable check
> bed drive all four stages — the build, the local run, the remote
> deploy, and the test harness. The binary that wires them together is
> also an MCP server, so your agent reaches every verb over the
> same RPC.

## Core concepts

A handful of ideas recur everywhere. Four of them are the heart of
OpenCharly — **candies & boxes**, **candyboxing**, **Risk Driven
Development**, and the **build → run → deploy → evaluate** lifecycle —
and the rest is the schema vocabulary that ties them together.

### Candies & boxes

OpenCharly treats boxes (container images) as composable building
blocks. Each **candy** is a self-contained unit; a **box** is an
ordered list of candies on top of a base. `charly` resolves the dependency graph, generates
multi-stage Containerfiles with cache mounts, and builds in the right
order — handling the hard parts so you (and your agents) don't
have to.

- **Candy** (`candy:` in `candy/<name>/charly.yml`) — packages (per-distro),
  `run:` plan steps (eight ops: `cmd`/`mkdir`/`copy`/`write`/`link`/`download`/
  `setcap`/`build`), services (one unified `service:` list — see
  init-system polymorphism below), volumes, env, ports, check probes,
  `env_provide`/`env_require`/`mcp_provide`/`mcp_accept` for
  cross-container discovery, plus a `version:` CalVer.
  → `/charly-image:layer`.
- **Image** — a `candy:` carrying `base:` + an ordered candy list. Multi-stage
  Containerfile, content-derived `ai.opencharly.version` OCI label,
  stable cache. → `/charly-image:image`.

### Candyboxing

Secure the *box* — a disposable, rootless container or VM with real,
kernel-enforced isolation — then hand your agent the whole candy store
inside it: every `charly` verb, every candy, every `charly check` probe, a real
system, a real GPU. Far more capability than a locked-down sandbox, and
a mistake costs one rebuild.
→ [VISION.md](VISION.md) (why), CLAUDE.md "Candyboxing" (the rule),
`/charly-internals:disposable` (the lifecycle boundary).

### Risk Driven Development (early)

Prove the riskiest unknown — above all whether a particular *combination*
of candies, at their latest versions, actually builds and runs together —
empirically on a disposable check bed EARLY, before a design rests
on it. `charly check` makes that proof cheap, for you and your agents alike.
→ [VISION.md](VISION.md) (why), CLAUDE.md "Risk Driven Development (RDD)"
(the rule), `/charly-check:check` (usage).

### Agent Driven Evaluation (acceptance)

What a box is *supposed* to do is written as a runnable `plan:` on the
candy that provides the behaviour, baked into the box as a label.
A `check:` step is verified deterministically; an `agent-check:` step
(prose only) is graded by an **agent** probing the live deployment. Author
by editing the candy's `plan:`, run with `charly box feature run` /
`charly check feature run`, or let the `charly check run` AI loop (an
`iterate:` bed) drive it to green. The spec is the test, and agents both
write it and grade it. Every candy MUST ship a non-empty `description:`
string AND a `plan:` with ≥1 deterministic `check:` step —
`charly box validate` hard-errors otherwise.
→ [VISION.md](VISION.md) (why), CLAUDE.md "Agent Driven Evaluation (ADE)"
(the rule), `/charly-check:check` (usage).

### Build → run → deploy → evaluate

The lifecycle is four verbs, and the same declarative inputs
flow through all of them:

- **Build** — a `candy:` with `base:` composes candies into a reproducible
  multi-stage image.
- **Run** — a `kind: pod` brings containers up as systemd-managed
  Podman quadlets.
- **Deploy** — `charly bundle add` applies the same candies to a host, VM,
  k8s cluster, or Android device — the substrate kind picks which.
- **Evaluate** — disposable check beds and baked `check:` checks prove any
  box or deployment works end-to-end.

See [Lifecycle](#lifecycle) for the full verb families (plus
authoring-with-agents and management).

### Schema kinds

A `charly.yml` is a recursive **name → kind** map: every key is an
entity NAME, and its value opens with exactly one **kind** — a reserved
discriminator word (`candy`, `pod`, `vm`, `k8s`, `local`,
`android`, `bundle`, plus the build vocabulary `distro` / `builder` /
`init` / `resource` / `sidecar` / `agent` / …). A kind's value is
**exactly one of** three shapes:

1. **a list of kinds** — composition (a box's candy list, a pod's
   container set);
2. **another name → kind map** — nesting (a resource deployed *into* a
   bundle, a sidecar *alongside* a pod; tree position *is* the deploy
   relationship);
3. **a reserved-word leaf** — the entity's own scalar params, typed by
   that kind's CUE definition.

This grammar is **enforced at load time**: every document is validated
against one closed CUE schema (`#NodeDoc`) *before* it executes — the
sole load gate — and a non-node-form document is rejected with a
`charly migrate` hint pointing at the one-shot upgrade.

**One schema, one source.** The schema lives in `sdk/schema/*.cue`.
The Go param structs, the reserved-word kind/verb vocabulary, and the
per-verb live-probe method allowlists are all
**generated / derived** from those `.cue` files by `task cue:gen` —
never hand-maintained in parallel. A reserved-word → Go-handler
registry, checked by a **startup bijection gate**, guarantees every
kind and verb in the schema is wired to exactly one Go handler, and
every handler to a schema word. Changing the schema is a **CUE-only
edit → `task cue:gen`**; the Go side follows
(recipe: `/charly-internals:go`).

Beyond `candy`, the deployable kinds are:

- **Pod** (`pod:`) — multi-container deploy shape: containers,
  sidecars, tunnels. Deployed as a Podman pod with a quadlet per
  container. → `/charly-pod:pod`.
- **VM** (`vm:`) — `source: {kind: cloud_image | bootc}`,
  disk/ram/cpu, libvirt+QEMU. `charly vm build/create/start/stop/clone/
  snapshot/console`. → `/charly-vm:vm`.
- **K8s** (`k8s:`) — Kubernetes cluster (in-pod k3s or external)
  with provisioning + workload defaults. → `/charly-kubernetes:kubernetes`.
- **Local** (`local:`) — host-side candy stack applied to the
  operator's machine (or any ssh-reachable host) via the native
  package manager + systemd + shell profile. → `/charly-local:local-spec`.
- **Android** (`android:`) — Android device: in-pod emulator
  (via `image:`) or remote/physical adb endpoint. `apk:` is a candy
  package format scoped to Android targets. → `/charly-check:android`.
- **Bundle** (`bundle:`) — a named deployment; the substrate kind
  (`pod:`/`vm:`/`k8s:`/`local:`/`android:`) picks where it lands (a
  `pod:` is a Podman pod by default), and the image it runs is the
  `image:` cross-ref. Carries env
  overlays, port remaps, volume backings, sidecars, tunnels, secrets,
  and the `disposable: true` opt-in. A `disposable: true` bundle is a
  *check bed* — the R10 test bed: `charly check run <bed>` runs build →
  deploy → probe → fresh-update → tear-down, and an `iterate:` block on
  it drives the agent-iteration harness (the AI scores the bed's
  `check:`/`agent-check:` steps, choosing among the configured
  `kind: agent` AI CLIs). → `/charly-core:deploy`, `/charly-check:check`.

### Cross-cutting rules

**`charly.yml` is the single project entry point.** Boxes are
discovered as `box/<name>/charly.yml`, candies as
`candy/<name>/charly.yml`, and the remaining kinds
(`vm`/`pod`/`k8s`/`bundle`/`local`/`android`) live inline in
`charly.yml`'s root; the distro/builder/init/resource build
vocabulary is embedded in the `charly` binary. `import:` composes
other files or repos — a bare string for a flat same-repo import
(legacy per-kind files like `box.yml` / `vm.yml` still load this
way, but are no longer the canonical layout), or a
single-key `alias: ref` map for a namespaced cross-repo import (Go
package-member semantics — `base: cachyos.cachyos`, fetched from
`@github.com/owner/repo:tag` and cached under `~/.cache/charly/repos/`).

**Init-system polymorphism — one place, no siblings.** A candy that
needs the same service under supervisord (containers) and systemd
(hosts / VMs / bootc) declares BOTH forms in one `service:` list,
same `name:`, one entry with `use_packaged: <unit>.service`, another
with custom `exec:`. The init system at deploy time picks the
matching form. *Never* create a `<name>-host` / `<name>-pod` sibling
candy for this. Canonical examples: `/charly-coder:sshd`,
`/charly-infrastructure:virtualization`, `/charly-infrastructure:postgresql`.

**Distro + package-format dispatch.** A candy declares `distro:` tag
sections (`fedora:43:` / `ubuntu:24.04:`) and package-format sections
(`rpm:` / `deb:` / `pac:` / `aur:` / `apk:`). A box declares its
`distro:` identity and `build:` formats. Distro tag first-match wins;
`build:` formats install in declared order. `fedora-coder` /
`arch-coder` / `debian-coder` / `ubuntu-coder` share ~30 candies,
differing only in package sections.

**Disposability — explicit opt-in.** `disposable: true` on a
`bundle` is the *one and only* authorization for `charly update`'s
autonomous destroy + rebuild. No hostname heuristic, no inference.
Explicit-only is what makes `charly update <name>` safe on shared
infrastructure. → `/charly-internals:disposable`.

## Why OpenCharly?

Containers are a great idea with rough edges. Real-world needs pile
up fast: GPU passthrough with the right driver stack, containers
that need `/dev/kvm` or virtualization access without blanket
`--privileged`, multiple services managed together, encrypted
volumes, VNC or browser-streamed desktops, device permissions that
don't compromise your host. Each is solvable — but solving them all
at once, reliably, across boxes, is where things get hard. And if
your agent has to build and manage these containers too, the
complexity compounds.

OpenCharly treats boxes as composable building blocks (see
[Core concepts](#core-concepts)) — handling the hard parts so you (and
your agents) don't have to.

**Testing and evaluating deployment configs is a first-class goal —
for you and your agents.** A deploy config is only useful if you can prove
it works, so any box or deployment is self-verifiable end-to-end — the
same surface whether you drive it at the keyboard or your agents drive
it autonomously. See [Evaluate](#evaluate) for the framework and
[Works with Claude Code](#works-with-claude-code) for the agents and
workflows. → `/charly-check:check`, `/charly-internals:agents`.

**Rootless-first power-user boxes.** The four boxes carrying the
full `charly` toolchain (`fedora-coder`, `charly-fedora`, `charly-arch`,
`githubrunner`) all run as uid=1000 with passwordless sudo. Four
cross-distro coder boxes (`/charly-coder:fedora-coder`/`arch-coder`/
`debian-coder`/`ubuntu-coder`) share ~30 candies, differing only in
package sections and how the uid-1000 user lands (create vs. adopt
mode). Rootless nested containers and rootless libvirt VMs work
with zero additive capabilities via the surgical `unmask=/proc/*`
security_opt from the `container-nesting` candy.
→ `/charly-distros:container-nesting`, `/charly-coder:fedora-coder`.

**Sandboxed agent desktops.** [Candyboxing](#candyboxing) applied to a
whole desktop: `/charly-openclaw:openclaw-desktop` is the all-in-one CachyOS
streaming desktop — Selkies desktop + openclaw-full gateway + agent CLIs
(claude-code, codex, gemini) + CPU ollama + nested `charly`. The agent (or the
user) builds boxes, launches nested rootless pods, and creates libvirt
VMs from a terminal inside the browser-accessible candybox desktop — uid 1000, no
`--privileged`, no added capabilities.

## Install

**Recommended — Go install** (requires Go 1.25.3+):

```bash
go install github.com/opencharly/charly/charly@latest
```

This puts `charly` in your `$GOPATH/bin`. Create an `charly.yml` and
a `candy/` directory and you're done. Legacy projects (predating
the unified schema, the `kind:` discriminators, or the singular
field names) convert in one shot with `charly migrate` — a single
idempotent chain to the latest CalVer schema. See `/charly-build:migrate`.

**Full project bootstrap** (to build boxes from this repo):

```bash
git clone --recurse-submodules https://github.com/opencharly/charly.git
cd opencharly
task build:charly         # on Arch: delegates to makepkg -si; elsewhere: portable install to ~/.local/bin/charly
charly box build        # build everything
```

**Arch / CachyOS / Manjaro** — install system-wide via `pacman`, building this
repo's bundled `opencharly-git` PKGBUILD (it is LOCAL-ONLY — NOT published to the
AUR):

```bash
cd pkg/arch && makepkg -si     # build + pacman-install opencharly-git from this repo
# or, equivalently, from the repo root:
task build:charly                  # pre-installs the AUR-only deps via your AUR helper, then runs makepkg -sefi in pkg/arch
```

The PKGBUILD `pkgver()` derives the same CalVer
(`YYYY.DDD.HHMM`) `charly version` prints, so `pacman -Q opencharly-git`
and `charly version` always agree. `depends=` covers the full runtime
surface — `podman`/`docker`/`fuse-overlayfs`/`slirp4netns` for
rootless containers, `qemu-full`/`libvirt`/`edk2-ovmf`/`swtpm` for
`charly vm`, `gnupg`/`pinentry`/`libsecret`/`gocryptfs`/`tailscale` for
secrets/encrypted volumes/tunnels, `go-task` so `task build:charly`
works from any fresh checkout. The pacman post-install hook enables
`docker.service` / `tailscaled.service` / `virtqemud.socket` and
adds the user to the `docker` and `libvirt` groups automatically.

**From source:**

```bash
cd charly && go build -o ../bin/charly .
```

## Quickstart

```bash
# Build a single box
charly box build fedora

# Build a CachyOS box (in submodule; charly resolves cross-repo refs)
charly -C box/cachyos box build cachyos

# Drop into an interactive shell
charly shell fedora

# Build and run a GPU-accelerated Jupyter server
charly box build jupyter
charly start jupyter

# Configure as a systemd service (quadlet + secrets + encrypted volumes)
charly config jupyter

# Build a bootable VM disk image from a bootc box
charly box build <my-bootc-box>             # a candy: with base: + bootc: true
charly vm build  <my-bootc-vm> --type qcow2 # a kind:vm with source.kind: bootc
charly vm create <my-bootc-vm>

# Apply candies directly to your workstation (no container)
charly bundle add host ripgrep
charly bundle add host fedora-coder --with-services --yes
charly bundle del host                  # reverses everything via ReverseOps + ledger

# Run a disposable check bed end-to-end (the R10 acceptance gate)
charly check run check-pod
```

## Lifecycle

The same six stages cover everything `charly` does — **build, run, deploy,
evaluate, author, manage**. Each maps to a family of `charly` verbs that
share the same declarative inputs.

### Build

> Declarative candy list → reproducible, fully-cached multi-stage
> image.

Each box declares a `base:`, an ordered `candy:` list, a `distro:`
identity, and a `build:` set of package formats. The planner
resolves the dependency graph, generates a multi-stage Containerfile
with cache-mounted package archives + AUR srcdest + pixi/npm/cargo
workdirs, and runs `podman build` (or `docker build` — switch with
`charly settings set engine.build podman`). Like conching chocolate, the
planner grinds every candy smooth — deduplicated, ordered, and
cache-warmed — before it sets into a box. The emitted image carries
OCI labels for every capability it claims: `ai.opencharly.description`
(the baked `plan:`), `ai.opencharly.check_level`,
`ai.opencharly.init`, `ai.opencharly.version` (content-derived
`EffectiveVersion`, stable across no-op rebuilds), `.ports`, etc.

Commands: `charly box build` (build), `charly box generate` (write
`.build/` only), `charly box validate`, `charly box inspect`,
`charly box list`, `charly box merge`, `charly box pull`,
`charly box reconcile`. MCP-driven authoring — `charly box {set,
add-candy, rm-candy, fetch, refresh, write, cat}`, `charly candy {set,
add-rpm, add-deb, add-pac, add-aur}` — gives agents
comment-preserving YAML edits over RPC.

Cross-repo refs: `import:` items and candy references can name
`@github.com/owner/repo:tag`. The resolver fetches every distinct
`(repo, git-tag)` and arbitrates per per-entity `version:` — same
`version:` across different git tags → silent (re-tag);
different `version:` → warn once and use the newest. `charly box
reconcile` aligns the cross-repo pins when a candy's CalVer moves.

→ `/charly-build:build`, `/charly-build:generate`, `/charly-build:validate`,
`/charly-build:inspect`, `/charly-build:reconcile`, `/charly-image:image`,
`/charly-image:layer`, `/charly-internals:capabilities`.

### Run

> Multiple containers, one declaration, one start command — as
> systemd-native units.

`kind: pod` is the multi-container deploy shape. `charly bundle add
<name> <pod-ref>` materializes it; `charly start` brings it up via
Podman quadlets (`~/.config/containers/systemd/`) so a deployment is
a real systemd user unit — `journalctl`, `systemctl status`,
auto-restart on failure, start on login. `charly stop`, `charly restart`,
`charly status`, `charly logs`, `charly cmd`, `charly shell`, and `charly service`
(drive the inner supervisord) operate it; `charly remove` deletes the
quadlet and containers.

Boxes with multiple co-resident services in one container use
supervisord as their init (declared via the same unified `service:`
list); boxes that deploy as separate containers get one quadlet
each in a shared pod. Either way, the same `service:` schema is the
input.

- **Multiple instances** (`-i <instance>`) — every command takes
  `-i`; instances get distinct quadlet names
  (`charly-<image>-<instance>.container`), `charly.yml` entries
  (`<image>/<instance>`), and disambiguated MCP server names.
- **Sidecars** (`--sidecar <name>`) — attach a Tailscale,
  cloudflare-tunnel, or other container template into a shared pod.
  Sidecar-related env (`TS_*`, `CF_*`) routes to the sidecar, not
  the app. List with `charly config --list-sidecars`.
- **Tunnels** — `tunnel:` block declares Cloudflare (public) or
  Tailscale (tailnet-private) exposure with full backend scheme
  support (HTTP / HTTPS / TCP / TLS / SSH / RDP / SMB).
- **Encrypted volumes** — `--encrypt <vol>` or `type: encrypted`;
  gocryptfs masterkey provisioned into the Secret Service, mounted
  via independent `charly-enc-<image>-<volume>.scope` systemd units
  that survive container restart. Manage with `charly config {mount,
  unmount, status, passwd}`.
- **GPU access** — NVIDIA via CDI (`gpu.nvidia.com` annotation);
  ROCm for AMD; `charly udev install/remove` writes the host-side
  rules. CUDA toolkit + cuDNN + ONNX Runtime in the `cuda` candy.
- **Wayland desktop streaming** — the Selkies family
  (`selkies-labwc`, `sway-desktop`, `sway-browser-vnc`) bundles a
  Wayland compositor (sway or labwc) + Chrome + `wayvnc` on port
  5900 + Pipewire audio. Browser pane at `:3000`.
- **Per-box MCP servers** — `chrome-devtools-mcp` on `:9224`,
  `jupyter-mcp` at `:8888/mcp`, `marimo-mcp` at `:2718/mcp/server`,
  nested `charly-mcp`. Declared via `mcp_provide:` and auto-discovered
  by consumers (Hermes, Claude Code) through `CHARLY_MCP_SERVERS`.
- **Auto service discovery** — a candy's `env_provide:` declares
  env vars with `{{.ContainerName}}` templates injected into every
  co-deployed container at `charly config` time. Deploy `ollama` and
  every other pod sees `OLLAMA_HOST=http://charly-ollama:11434`.
  `mcp_provide:` works the same way for MCP URLs.
  `env_require:` / `env_accept:` document consumer dependencies
  so `charly config` warns early.

→ `/charly-core:start`, `/charly-core:logs`, `/charly-core:cmd`,
`/charly-core:service`, `/charly-core:charly-status`, `/charly-automation:sidecar`,
`/charly-automation:enc`, `/charly-automation:udev`, `/charly-pod:pod`,
`/charly-selkies:selkies-desktop-layer`, `/charly-selkies:sway`.

### Deploy

> The same `charly.yml` applied to a host, a remote ssh box, a VM, a
> k3s cluster, or an Android device.

`charly bundle add <name> <ref>` is the unified verb; the substrate
kind (`pod:`/`vm:`/`k8s:`/`local:`/`android:`) discriminates
where it lands, and the image it runs is the `image:` cross-ref:

- **`pod:`** (default) — Podman + quadlet, as in [Run](#run).
- **`vm:`** — libvirt + QEMU. Candies are applied *inside* the
  booted VM over SSH via the same InstallPlan IR. `charly vm build`
  (bootc → QCOW2/RAW), `charly vm create/destroy/start/stop`, `charly vm
  clone` (snapshot fork), `charly vm snapshot`, `charly vm console`. The
  managed `~/.config/charly/ssh_config` fragment gets a `Host
  charly-<vmname>` stanza written on `charly vm create`.
  → `/charly-vm:vm`, `/charly-internals:vm-deploy-target`.
- **`k8s:`** — Kustomize tree applied to k3s in-pod (candy
  triplet `/charly-infrastructure:k3s` + `k3s-server` + `k3s-agent`) or
  to an external cluster. `K3S_CLUSTER_TOKEN` auto-generated on
  first deploy via `ensureCandySecret` and shared with every joining
  agent — zero operator setup. → `/charly-kubernetes:kubernetes`,
  `/charly-infrastructure:k3s`.
- **`local:`** — applies the candies' packages / files /
  systemd units to the host filesystem. `host: local` (default)
  uses the local shell executor; `host: user@machine[:port]` (or a
  configured alias) re-execs `charly` over SSH. Per-machine overlays
  via `add_candy:` in `~/.config/charly/charly.yml`. Ledger at
  `~/.config/opencharly/installed/` records every ReverseOp so
  `charly bundle del host` reverses precisely what was applied.
  → `/charly-local:local-deploy`, `/charly-local:local-spec`.
- **`android:`** — `kind: android` device (in-pod emulator
  via `image:` or remote adb endpoint via `adb: {host: …}`);
  `apk:` packages installed by `apkeep` (Google Play) or pushed
  from committed `.apk` files via goadb. Nested `pod → android`
  mirrors `vm → k8s`. → `/charly-check:android`, `/charly-check:adb`.

`charly bundle del`, `charly bundle sync` (apply K8s changes live),
`charly bundle from-box` (source-less deploy from OCI labels), and
`charly update` complete the lifecycle. `charly update <name>` performs
destroy + (optional rebuild) + create + start unattended *only*
when the deploy carries `disposable: true`.

**Secrets.** Credentials resolve in order: env var → Secret Service
(systemd keyring; GNOME Keyring, KDE Wallet, or KeePassXC
FdoSecrets) → config-file fallback (`~/.config/charly/config.yml`,
0600). Project-level shell secrets live in a GPG-encrypted
`.secrets` file: `charly secrets gpg env` decrypts in memory when
direnv loads the project; no plaintext on disk. Manage with `charly
secrets gpg {env, show, set, unset, edit, encrypt, recipients,
import-key, export-key, setup, doctor}`. Candy-private secrets
(like `K3S_CLUSTER_TOKEN`) get auto-provisioned via
`ensureCandySecret` and stored under `charly/secret/<key>` in the
Secret Service. **Agent forwarding** — the `agent-forwarding` candy
binds host `SSH_AUTH_SOCK` / `GPG_AGENT_SOCK` into the container.
→ `/charly-build:secrets`.

→ `/charly-core:deploy`, `/charly-core:charly-config`, `/charly-core:charly-update`,
`/charly-internals:disposable`, `/charly-vm:vms-catalog`.

### Evaluate

> Build → deploy → probe → fresh-update → tear down — disposable beds
> with the same DSL as production deploys.

Tests are first-class. Every `charly.yml` (box + candy) declares its
plan as an ordered set of child step nodes, each carrying exactly one
intent keyword — `run:` (deterministic state-change, the install timeline),
`check:` (deterministic idempotent probe), `agent-run:` (an agent that
may mutate), `agent-check:` (read-only agent assessment), or
`include: <kind>:<name>` (splice another entity's plan) — plus an inline
`Op` and a `context:`. `check:` covers the goss-style probes (files,
packages, services, ports, processes, commands, HTTP, DNS, mounts, users,
groups, kernel params, interfaces, matchers); `run:` covers configuration
(install a package, write a file, drive a UI); `agent-check:` carries
free-form prose graded by an agent. The plan bakes into a three-section
OCI label (`ai.opencharly.description` → `{candy, box, deploy}`) so any
pulled box is self-testable without its source repo.

Three execution modes:

- **`charly check box <image>`** — disposable `podman run --rm` of the
  baked layer + image checks. Build-scope; no deploy state.
- **`charly check live <image>`** — runs all three sections against a
  *running* deployment, substituting deploy-time variables
  (`${HOST_PORT:N}`, `${VOLUME_PATH:name}`, `${CONTAINER_IP}`,
  `${ENV_*}`) so the same check survives port remaps and volume
  rebindings.
- **`charly check run <bed>`** — the canonical R10 acceptance gate.
  Picks a disposable check bed from the project `charly.yml` (a bundle
  carrying `disposable: true`) and runs build → check box → deploy
  → check live → fresh `charly update` → check live again → teardown.
  Pick the bed whose kind matches what you changed: `check-pod`,
  `check-local`, `check-k3s-vm`, `check-android-emulator-pod`.
  To run a whole roster, fan the beds out concurrently — one
  `charly check run <bed>` per agent — via the `/verify-beds` workflow.

Exit codes are goss-style: `0` = all checks passed, `1` =
infra/usage error (the check never reached a verdict), `2` =
checks failed. R10 automation treats `1` as "did not run",
not "failed".

**Agents drive these beds.** Claude Code sub-agents
(`check-bed-runner`, `deploy-verifier`) and dynamic workflows
(`/verify-beds`, `/audit-deploy-configs`) run `charly check
run`/`live`/`box` against the existing beds and return verbatim
pass/fail — the same disposable-bed verification, whether you run it
or your agent does. → `/charly-internals:agents`.

Eleven live-container probe verbs — authorable inline as `plan:`
`check:` steps (`cdp: check`, `wl:
screenshot`, `dbus: call`, `vnc: status`, `mcp: list-tools`, `adb:
getprop`, `appium: click`, …):

- `cdp:` — Chrome DevTools Protocol declarative check verb (open, click,
  check JS, screenshot) — out-of-process plugin (`candy/plugin-cdp`), no host
  CLI subcommand.
- `wl:` — Wayland / sway / labwc declarative check verb (input, windows,
  screenshots, sway IPC, overlays, atspi, clipboard) — out-of-process plugin
  (`candy/plugin-wl`), no host CLI subcommand.
- `dbus:` — D-Bus declarative check verb (list, call, introspect, notify) —
  out-of-process plugin (`candy/plugin-dbus`), no host CLI subcommand.
- `vnc:` — RFB/VNC declarative check verb (status, screenshot,
  pointer/keyboard) — out-of-process plugin (`candy/plugin-vnc`), no host CLI
  subcommand; covers pod (port 5900) AND vm (libvirt VNC) targets.
- `mcp:` — Model Context Protocol declarative check verb (list-tools,
  list-resources, read-resource, call-tool) — out-of-process plugin
  (`candy/plugin-mcp`), no host CLI subcommand.
- `libvirt:` — libvirt-RPC declarative check verb (VM info, screenshot,
  send-key, QMP, snapshots, event stream) — out-of-process plugin
  (`candy/plugin-vm`), no host CLI subcommand. VM-only.
- `record:` — recording-session declarative check verb (start/stop/list/cmd;
  terminal asciinema or desktop wf-recorder/pixelflux) — out-of-process plugin
  (`candy/plugin-record`), no host CLI subcommand. Container-only.
- `spice:` — SPICE wire-level display-protocol declarative check verb
  (status, screenshot, cursor, click, mouse, type, key) — out-of-process
  plugin (`candy/plugin-spice`), no host CLI subcommand. VM-only.
- `kube:` — Kubernetes cluster-probe declarative check verb (nodes, pods,
  ingress, wait-ready, storageclass, addons, apply/delete, raw resource
  GETs) — out-of-process plugin (`candy/plugin-kube`), no host CLI
  subcommand.
- `adb:` — Android Debug Bridge declarative check verb (devices, shell,
  install, getprop, screencap, logcat, wait-for-device) — out-of-process
  plugin, no host CLI subcommand.
- `appium:` — W3C WebDriver declarative check verb (session lifecycle, find,
  click, send-keys, screenshot) — out-of-process plugin, no host CLI
  subcommand.

`charly feature {list, pending, validate}` enumerates and validates the
`plan:` steps on the same entries (`pending` lists the agent-graded
`agent-run:`/`agent-check:` steps).

→ `/charly-check:check`, `/charly-check:cdp`, `/charly-check:wl`, `/charly-check:dbus`,
`/charly-check:vnc`, `/charly-check:spice`, `/charly-check:libvirt`,
`/charly-check:record`, `/charly-kubernetes:check-k8s`, `/charly-check:adb`,
`/charly-check:appium`, `/charly-check:android`.

### Author with agents

> Agents in the loop, authoring and iterating on candies and
> boxes — `charly`-specific.

The agent iteration harness sits on top of a disposable check bed via two
pieces — the `kind: agent` catalog and an `iterate:` block:

- **`kind: agent`** — reusable agent CLI catalog (`claude`,
  `codex`, `gemini`, …). Each entry declares a command, a version
  probe, an output format (typically `stream-json`), and credential
  paths. The harness parses each NDJSON line into
  `iteration[].runner_event`.
- **`iterate:` block** — the AI-loop orchestration declared on a
  disposable check bed (or any `bundle`): the eligible agents, the
  `sandbox:` where the agent + `charly` run (the former
  `check-sandbox`), the plateau iteration count, the prompt, and the
  watchdog timeout. The bed's own `plan:` IS the scored content —
  `include: <kind>:<name>` splices in another entity's plan, and the
  `check:`/`agent-check:` steps are the scored success criteria.
  `charly check run <bed>` runs the multi-hour benchmark when the bed
  carries an `iterate:` block: the agent reads scope
  (`charly check scope`) + prior tag (`charly check last-tag`) + live results →
  rebuilds + redeploys → harness re-scores → continues until plateau
  detection or the watchdog fires. Progressive disclosure means the
  agent earns plan steps one at a time.

Cross-cutting: **`charly mcp serve`** is the MCP gateway. Every leaf
Kong command auto-exposes as an MCP tool (Streamable HTTP or
stdio), so Claude Code, Codex, or any MCP client drives the full
`charly` surface over RPC. `--read-only` filters destructive tools;
auto-fallback to `opencharly/charly` when no project is wired
(opt out with `--no-default-repo`).

→ `/charly-check:check`, `/charly-build:charly-mcp-cmd`, `/charly-coder:charly-mcp`,
`/charly-coder:claude-code`, `/charly-coder:codex`, `/charly-coder:gemini`.

### Manage

> Ops verbs: cleanup, diagnostics, schema upgrades, runtime config,
> host-side aliases.

- `charly clean` — prune build artifacts by CalVer retention
  (`keep_images`, `keep_check_runs`); sweeps stale makepkg
  leftovers. Label-CalVer wins over tag-CalVer.
- `charly doctor` — host dependency check (`podman`/`docker`/`libvirt`/
  `qemu`/`gnupg`/`gocryptfs`/`tailscale`/…).
- `charly reap-orphans` — find ephemeral deployments whose underlying
  pod/vm/scope is gone and remove the stale quadlet.
- `charly migrate` — single idempotent chain to the latest CalVer
  schema. Auto-invoked on remote-cache downloads. The
  `LatestSchemaVersion()` gate hard-errors newer-than-binary
  configs.
- `charly settings {get, set, list, reset, path, migrate-secrets}` —
  engine (`engine.build podman|docker`), secret backend, host
  aliases (`hosts.<name> user@machine`), VM backend.
- `charly version` — print computed CalVer tag.
- `charly tmux {ls, attach}` — drive tmux sessions inside containers.
- `charly ssh tunnel {spice, vnc, …}` — forward SPICE/VNC/unix sockets
  from a remote libvirt host to the local machine.
- `charly alias install` — register box-scoped shell aliases
  (bash/zsh/fish) so `<image>` on the host transparently runs
  inside the container.
- `charly udev install/remove` — host-side udev rules for GPU device
  access (CDI symlinks).

→ `/charly-core:clean`, `/charly-core:charly-doctor`, `/charly-core:charly-update`,
`/charly-build:migrate`, `/charly-build:settings`, `/charly-core:ssh`,
`/charly-automation:tmux`, `/charly-automation:alias`,
`/charly-automation:udev`.

## Command reference

The `charly` CLI has 29 top-level verbs across three modes with disjoint
input sets — **build mode** (`charly box …` reads `charly.yml`),
**test mode** (`charly check …` reads OCI labels + `charly.yml` overlays,
never `charly.yml`), and **deploy mode** (everything else reads
OCI labels + `charly.yml`) — plus the cross-mode `charly mcp serve`
gateway exposing the entire surface as MCP tools.

| Area | Commands | Skill |
|---|---|---|
| **Box (build mode)** | `charly box {build, generate, validate, merge, new, inspect, list, pull, reconcile}` | `/charly-image:image` + `/charly-build:build`, `/charly-build:generate`, `/charly-build:validate`, `/charly-build:merge`, `/charly-build:new`, `/charly-build:inspect`, `/charly-build:list`, `/charly-build:pull`, `/charly-build:reconcile` |
| **Box authoring (MCP-first)** | `charly box {set, add-candy, rm-candy, fetch, refresh, write, cat}` and `charly candy {set, add-rpm, add-deb, add-pac, add-aur}` | `/charly-image:image` "Authoring" + `/charly-image:layer` |
| **Deployment** | `charly bundle {add, del, sync, from-box, export, import, show, reset, status, path}`; `charly config`; `charly start`, `charly stop`, `charly restart`, `charly update`, `charly remove` | `/charly-core:deploy`, `/charly-core:charly-config`, `/charly-core:start`, `/charly-core:stop`, `/charly-core:charly-update`, `/charly-core:remove`, `/charly-local:local-deploy`, `/charly-kubernetes:kubernetes`, `/charly-internals:vm-deploy-target` |
| **Runtime** | `charly shell`, `charly cmd`, `charly service`, `charly status`, `charly logs`, `charly tmux` | `/charly-core:shell`, `/charly-core:cmd`, `/charly-core:service`, `/charly-core:charly-status`, `/charly-core:logs`, `/charly-automation:tmux` |
| **Test + probes** | `charly check {box, live, run}` + the 11 live probe verbs (`cdp`, `wl`, `dbus`, `vnc`, `mcp`, `record`, `spice`, `libvirt`, `k8s`, `adb`, `appium`); `charly feature {list, pending, validate}` | `/charly-check:check`, `/charly-check:cdp`, `/charly-check:wl`, `/charly-check:dbus`, `/charly-check:vnc`, `/charly-check:spice`, `/charly-check:libvirt`, `/charly-check:record`, `/charly-kubernetes:check-k8s`, `/charly-check:adb`, `/charly-check:appium` |
| **MCP gateway** | `charly mcp {serve, ping, servers, list-tools, list-resources, list-prompts, call, read}` | `/charly-build:charly-mcp-cmd`, `/charly-coder:charly-mcp` |
| **VM** | `charly vm {build, create, start, stop, destroy, snapshot, clone, console, ssh, import, list}` | `/charly-vm:vm`, `/charly-vm:vms-catalog`, `/charly-internals:vm-deploy-target` |
| **Schema migration** | `charly migrate` (single idempotent chain) | `/charly-build:migrate` |
| **Secrets & config** | `charly secrets`, `charly settings`, `charly alias`, `charly udev` | `/charly-build:secrets`, `/charly-build:settings`, `/charly-automation:alias`, `/charly-automation:udev` |
| **Host & admin** | `charly doctor`, `charly clean`, `charly reap-orphans`, `charly ssh`, `charly version` | `/charly-core:charly-doctor`, `/charly-core:clean`, `/charly-core:ssh`, `/charly-core:charly-version` |

**Global flags** (apply to every command):

- `-C <dir>` / `--dir <dir>` / `CHARLY_PROJECT_DIR=<dir>` — override the
  project directory.
- `--repo <OWNER/REPO[@REF]>` / `CHARLY_PROJECT_REPO=…` — read
  `charly.yml` from a remote git repo. Bare `owner/repo`
  auto-prefixes `github.com/`; the literal `default` expands to
  `opencharly/charly`. Cached in `~/.cache/charly/repos/`. Mutually
  exclusive with `--dir`.
- `--host <alias|user@machine[:port]>` / `CHARLY_HOST=…` — re-exec the
  command on a remote host over SSH. Commands marked LocalOnly
  (`settings`, `version`, `ssh tunnel`) always run locally.

## Catalogs

Content lives in the working tree and in the skill index — pointers,
not enumerations:

- **Candy library** (`candy/` + submodule `box/<distro>/candy/`,
  187 candies total). Foundation: `/charly-distros:*` (40 skills — base
  OS, GPU runtime, bootc, per-distro builders),
  `/charly-languages:*`, `/charly-infrastructure:*` (22), `/charly-tools:*`
  (19). Per-pod: `/charly-jupyter:*`, `/charly-coder:*` (33),
  `/charly-selkies:*` (40), `/charly-openclaw:*`, `/charly-versa:*`,
  `/charly-ollama:*`, `/charly-openwebui:*`, `/charly-comfyui:*`,
  `/charly-immich:*`, `/charly-hermes:*`, `/charly-filebrowser:*`.
- **Box catalog** (discovered `box/<name>/charly.yml` in the `box/<distro>` submodules — main owns none after the 2026-06 box inversion) — boxes,
  39 enabled by default. Same plugin namespaces; per-pod boxes
  carry an MCP server hint in `plugins/README.md`.
- **VM catalog** (`vm.yml` + `box/cachyos/vm.yml`) — cloud_image
  + bootc entries. → `/charly-vm:vms-catalog`.
- **Deploy-target catalog** — pod / vm / k8s / local / android.
  Each has a dedicated kind file.
- **Check bed catalog** (the disposable-bundle beds in the project and
  `box/<distro>` `charly.yml`s) — disposable check beds for R10,
  plus `kind: agent` and the bed's `iterate:` block for the agent
  harness. → `/charly-check:check`.

Candies used by only one box family are vendored in that
`box/<distro>` submodule (e.g. `ghostty`/`keepassxc-keyring` in
`box/cachyos`, `arch-*-test` fixtures in `box/arch`). Shared
candies are pulled by `@github` ref.

**Composition meta-candies** — `sway-desktop`, `sway-desktop-vnc`,
`selkies-desktop`, `openclaw-full`, `openclaw-full-ml`,
`python-ml`, `jupyter-ml`, `unsloth-studio` bundle curated candy
sets.

**Data candies / data boxes** — `data:` block in `charly.yml` stages
files at `/data/<volume>/`; `charly config --bind <volume>` provisions
them at deploy time; `charly update` merges new data non-destructively.
`data_image: true` scratch-based boxes carry data + OCI labels,
consumed via `charly config --data-from <data-image>`.

See `plugins/README.md` for the authoritative skill index and this repo's
[`CHANGELOG/`](CHANGELOG/README.md) (one file per CalVer version) for the dated history of cutovers.

## Troubleshooting

Each entry points to the canonical skill — details belong there,
not here.

| Symptom | First step |
|---------|-----------|
| Service won't start | `charly status <image>` then `charly logs <image>` (`/charly-core:charly-status`, `/charly-core:logs`) |
| Quadlet out of sync with charly.yml | `charly config <image> --update-all` (`/charly-core:charly-config`) |
| Build cache stale | `charly box build --no-cache <image>` (`/charly-build:build`) |
| Chrome stuck or crash-looping | `/charly-selkies:chrome` Resource Caps & Circuit Breaker section |
| Encrypted volume locked at boot | `charly config mount` waits for keyring unlock automatically — zero CPU, event-driven (`/charly-automation:enc`) |
| GPU not detected | `charly doctor` then `/charly-automation:udev` |
| Tunnel not appearing on a new instance | Tunnel config is `charly.yml`-only — add manually per instance (`/charly-core:deploy`) |
| Service built fine but broken in production | `charly check live <image>` runs the baked layer + image + deploy checks (`/charly-check:check`) |
| `charly vm build` fails: "no kind:vm entity in vm.yml" | Declare a `kind: vm` entity (`/charly-vm:vms-catalog`) |
| SPICE console blank on cloud_image VM | Known `simpledrm → qxldrmfb` race under UEFI; switch to `firmware: bios` (`/charly-vm:arch`) |
| `charly bundle add vm:<name>` errors "VM does not exist" | Run `charly vm create <name>` first — VM deploy is not auto-provisioning (`/charly-core:deploy`) |
| Resolver "referenced at multiple versions" warning | `charly box reconcile` aligns the cross-repo `@github` pins (`/charly-build:reconcile`) |
| `charly box pull` says "image is not available locally" | `charly box pull` accepts short name + project, fully-qualified ref, or `@github` remote ref. See `/charly-build:pull` |
| Newer-than-binary config rejected at load | `charly migrate` brings the project to the latest schema CalVer (`/charly-build:migrate`) |
| Schema/format change won't apply | `charly migrate` is idempotent; auto-invoked on remote-cache fetches |

## Adding a candy

```bash
charly box new candy my-candy             # Scaffold the directory
# Edit candy/my-candy/charly.yml        # Declare packages, deps, env, ports,
#                                       # services, check probes, and run: steps
#                                       # (see /charly-image:layer for the verb catalog)
# Optionally add pixi.toml / package.json / Cargo.toml for auto-detected builders.

# Add to a box's composition in box/<name>/charly.yml — a child node:
#   my-box-candy:
#       candy: [..., my-candy]

charly box build my-image                 # Build it
charly check box my-image                  # Run the baked checks
```

`/charly-image:layer` is the canonical reference for the eight
`run:`-step verbs (`command`, `mkdir`, `copy`, `write`, `link`,
`download`, `setcap`, `build`), the unified `service:` schema, `vars:`
substitution, YAML anchors, and execution-order rules.
`/charly-check:check` covers the matcher forms, runtime variable table,
gold-standard pattern (`candy/redis/charly.yml`), and the 10
authoring gotchas.

## Works with Claude Code

OpenCharly works hand-in-hand with
[Claude Code](https://claude.com/claude-code). The bundled
[plugins/](plugins/) directory provides skills that teach Claude
how to compose, build, deploy, and manage your boxes.
Every candy, every box, every command has a dedicated skill.

**Quick setup** — add this to your project's `.claude/settings.json`:

```json
{
  "enabledPlugins": {
    "charly-core@charly-plugins": true,
    "charly-build@charly-plugins": true,
    "charly-check@charly-plugins": true,
    "charly-image@charly-plugins": true,
    "charly-internals@charly-plugins": true,
    "charly-distros@charly-plugins": true,
    "charly-infrastructure@charly-plugins": true,
    "charly-jupyter@charly-plugins": true,
    "charly-coder@charly-plugins": true
  },
  "extraKnownMarketplaces": {
    "charly-plugins": {
      "source": { "source": "directory", "path": "./plugins" }
    }
  }
}
```

Representative subset; see `plugins/.claude-plugin/marketplace.json`
for the full 25-plugin catalog. Clone with submodules to get the
plugins directory: `git clone --recurse-submodules
https://github.com/opencharly/charly.git`.

**MCP gateway as the universal channel.** `charly mcp serve` exposes
every `charly` CLI leaf as an MCP tool (Streamable HTTP or stdio), so
the agent reaches the full build / deploy / test surface over
RPC. Per-box MCP servers (chrome-devtools-mcp, jupyter-mcp,
marimo-mcp, charly-mcp) auto-discover via `mcp_provide:` when their
containers are running.

**Sub-agents, dynamic workflows, and agent teams.** Beyond skills, the
project ships Claude Code **sub-agents** (`plugins/internals/agents/`):
executors `check-bed-runner` and `deploy-verifier` that drive the `charly check`
beds and return verbatim proof, plus enforcers `root-cause-analyzer`,
`testing-validator`, and `layer-validator`. Two **dynamic workflows**
(`.claude/workflows/`) fan the work out — `/verify-beds` runs every
disposable check bed as the R10 gate, `/audit-deploy-configs` evaluates your
deploy configs — and the same agent definitions reuse as **agent-team**
teammates. Whether you drive `charly` from the keyboard or hand it to an
agent, testing and verifying deployments uses the one surface.
→ `/charly-internals:agents`.

See [VISION.md](VISION.md) for the long-term thesis and direction,
[CLAUDE.md](CLAUDE.md) for the project's rules and mandates,
[plugins/README.md](plugins/README.md) for the full skill index (usage
and architecture live in the skills), and this repo's [CHANGELOG/](CHANGELOG/README.md)
for dated history (one file per CalVer version; by policy, never duplicated here or in skills).

## License

MIT
