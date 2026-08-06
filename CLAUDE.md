# OpenCharly — The Fully Stocked Gourmet Kitchen for You and Your Agents

Compose, build, deploy, and evaluate **boxes** (container images) from a library of configurable **candies**, driven by the `charly` Go CLI — built for you *and* your agents, on Docker, Podman, QEMU, libvirt, Kubernetes, and Android.

This file is the Claude Code adapter of the project rulebook: mandates and pointers only, load-bearing and non-negotiable — short so they are always read, never because they are optional. `AGENTS.md` is the equivalent harness-neutral rulebook; skills own detailed procedure; history lives in each repo's `CHANGELOG/`.

## R0. Skills first

Before reading source, running a repository command, delegating, planning, or editing, load every skill the dispatcher selects — all matching rows in one message via the `Skill` tool. Acting first is a protocol violation: stop, load, continue. Consult order: skills → CLAUDE.md → memory → code exploration; for a high-risk claim a live `disposable: true` bed outranks every document (RDD) — the skill is the first hypothesis, never the final verdict.

## Skill Dispatcher

Consult this table before the first tool call of every task; when several rows match, load all their skills in one message.

| Trigger (what the user said or what you're about to do) | Skills to load BEFORE doing anything |
|---|---|
| **— Build & author boxes and candies —** | |
| Editing a candy (`candy/<name>/charly.yml`), candy authoring, candy tasks/services | `/charly-image:layer` |
| Editing a box (`box/<name>/charly.yml` — boxes live in the `box/<distro>` submodules; main owns none), box composition | `/charly-image:image` |
| Authoring a plugin (a candy with a `plugin:` block) / builtin vs out-of-tree plugin / per-plugin `.cue` schema (single source → `gengotypes` for dev + schema-over-`Describe` RPC at runtime) / the plugin SDK (`github.com/opencharly/sdk`, the `sdk/` submodule) / `sdk/**` / a compiled-in plugin candy (`compiled_plugins:`) or host-coupled kit candy / an external plugin module | `/charly-internals:plugin` |
| `charly box build` / `charly box generate` / Containerfile | `/charly-build:build` + `/charly-build:generate` + `/charly-internals:generate-source` |
| `charly box validate` / schema error | `/charly-build:validate` |
| `charly migrate` / schema migration / legacy → latest CalVer / CalVer schema version | `/charly-build:migrate` |
| `charly box reconcile` / cross-repo `@github` pin alignment / candy-version-mismatch cleanup | `/charly-build:reconcile` |
| Secret management / `charly secrets` / Secret Service / GPG `.secrets` | `/charly-build:secrets` |
| `charly clean` / build-artifact retention / `keep_images` / `keep_check_runs` / image-tag pruning / `.check` run cleanup | `/charly-core:clean` |
| `charly docs` / the opencharly.ai site / the `docs/` submodule / `task docs:sync`/`docs:drift` / Starlight/Astro / `candy/plugin-docs` (runtime plugin) or `candy/docs-site` | `/charly-build:docs` + `/charly-tools:docs-site` |
| **— Deploy & run —** | |
| `charly update` / `charly vm *` / VM entities in `vm.yml` or `vm:` | `/charly-vm:vm` + `/charly-internals:vm-deploy-target` |
| `charly bundle add/del` / pod or container deploys | `/charly-core:deploy` |
| local-target deploy / `target: local` / `host: local` (default) / SSH-host deploys / `user:` / `ssh_arg:` | `/charly-local:local-deploy` + `/charly-internals:local-infra` |
| Editing `local.yml` / authoring `kind: local` templates | `/charly-local:local-spec` |
| Managed `~/.config/charly/ssh_config` fragment / `charly vm create` writes Host stanza | `/charly-vm:vm` + `/charly-local:local-deploy` |
| `kind: android` device / `target: android` deploy / `apk:` package format in candies / installing Android apps declaratively / remote-or-emulator adb endpoint / nested `pod → android` | `/charly-check:android` + `/charly-core:deploy` |
| Disposable-flag semantics / `disposable: true` authorization / preemptible-flag / `requires_exclusive:` / `charly preempt` / exclusive host-resource arbitration (GPU passthrough contention) | `/charly-internals:disposable` (+ `/charly-core:deploy` for arbitration) |
| **— Evaluate & verify —** | |
| `charly check *` (ANY check verb, incl. `charly check box`) / `charly check run <bed>` (the disposable-deploy R10 bed) / authoring `disposable: true` check beds / `charly check live` / the probe verbs (cdp/wl/dbus/vnc/mcp/record/spice/libvirt) / `iterate:` AI-agent scoring / `plan:` step authoring / `charlycheck/*` branches | `/charly-check:check` |
| Agent Driven Evaluation (ADE) / `charly box feature run` / `charly check feature run` / `charly feature list/pending/validate` / authoring a candy's `plan:` + `description:` string / the agent grader for `agent-check:` steps | `/charly-check:check` + `/charly-internals:strict-policy` |
| the `kube:` check verb / Kubernetes cluster probing from a candy/box plan (out-of-process plugin; nodes, pods, ingress, wait-ready, storageclass, addons, apply/delete, raw resource GETs) | `/charly-kubernetes:check-k8s` |
| the `adb:` check verb / Android Debug Bridge probing from a candy/box plan (out-of-process plugin; devices, shell, install, getprop, screencap, logcat, wait-for-device) | `/charly-check:adb` + `/charly-check:check` |
| the `appium:` check verb / Android UI automation (out-of-process plugin) / W3C WebDriver sessions, element introspection, the gesture/app/key/device sugar groups, the generic `execute`/`raw` escape hatch | `/charly-check:appium` + `/charly-check:check` |
| Verify a cutover by running the R10 beds (drive `charly check run <bed>`) | `/charly-internals:agents` + `/charly-check:check` (agent `check-bed-runner`, workflow `/verify-beds`) |
| Evaluate/audit a deployment config (image or deploy, yours) | `/charly-internals:agents` + `/charly-check:check` (agent `deploy-verifier`, workflow `/audit-deploy-configs`) |
| **— Git & landing —** | |
| Git/`gh` workflow — `feat/` branch, commit, PR-only landing (NO direct push to main), branch protection, the `pr-validator` fresh-evaluator merge/tag, CalVer-at-merge, worktree, sync-to-upstream, branch/worktree prune, cross-repo R10 landing | `/charly-internals:git-workflow` |
| **— Discipline & process —** | |
| Hard-cutover concerns / rename sweeps | `/charly-internals:cutover-policy` |
| Engineering-discipline triggers (failure surfaced / dup pattern / ad-hoc fix tempting / "out of scope" framing) | `/charly-internals:strict-policy` |
| Unexpected failure / error / anomaly | `/charly-internals:root-cause-analyzer` agent (BEFORE any fix) |
| **— Go & internals —** | |
| Go source work (adding/modifying `charly` commands) | `/charly-internals:go` |
| Editing `sdk/schema/*.cue` / `task cue:gen` / `cue exp gengotypes` / generated `cue_types_gen.go` / Schema Driven Design (SDD) / a schema spike | `/charly-internals:go` + `/charly-internals:plugin` |
| Go code-quality / CLAUDE.md-compliance audit / `golangci-lint` / `dupl` / duplication or dead-code check / `.golangci.yml` | `/charly-internals:go-quality` + `/charly-internals:strict-policy` |
| IR / InstallPlan / DeployTarget / OCITarget | `/charly-internals:install-plan` |
| OCI labels / capabilities contract | `/charly-internals:capabilities` |
| Egress config validation — validating/generating the config files charly WRITES to a system (`candy/plugin-bundle/egress.go`, `ValidateEgress`, the vendored CUE egress schemas in `candy/plugin-egress/egress-schemas/vendor/`, cloud-init/k8s/units/ssh_config/libvirt-XML egress) | `/charly-internals:egress` |
| VmSpec / libvirt / cloud-init / OVMF internals | `/charly-internals:vm-spec` (+ renderer skills as needed) |
| **— Orientation: "what does candy X do?" / "what's in box X?" —** | |
| Pod apps, language runtimes, infrastructure services, CLI utilities / the `charly` binary | `/charly-<family>:<name>` — families: `jupyter`, `coder`, `selkies`, `openclaw`, `versa`, `ollama`, `openwebui`, `comfyui`, `immich`, `hermes`, `filebrowser` (pod apps); `languages` (python, python-ml, pixi); `infrastructure` (postgresql, redis, k3s, traefik, supervisord, tailscale, gocryptfs, virtualization, dbus-layer, tmux-layer, …); `tools` (ripgrep, himalaya, whisper, charly, …) |
| Base distros / GPU runtime | `/charly-distros:<name>` (arch, fedora, debian, ubuntu, cachyos, nvidia, cuda, rocm, …) |
| CachyOS images / `cachyos*` / `charly-cachyos` workstation profile / `box/cachyos` submodule | `/charly-distros:cachyos` + `/charly-vm:cachyos-bootstrap-vm` + `/charly-local:charly-cachyos` |
| Debian images / `debian*` / `box/debian` submodule | `/charly-distros:debian` + `/charly-distros:debian-builder` + `/charly-distros:debian-debootstrap` + `/charly-coder:debian-coder` + `/charly-vm:debian-debootstrap-vm` |
| Ubuntu images / `ubuntu*` / `box/ubuntu` submodule | `/charly-distros:ubuntu` + `/charly-distros:ubuntu-builder` + `/charly-distros:ubuntu-debootstrap` + `/charly-coder:ubuntu-coder` + `/charly-vm:ubuntu-debootstrap-vm` |
| Fedora images / `fedora*` / `box/fedora` submodule (incl. the GPU base `nvidia` / `python-ml` + `sway-browser-vnc`) | `/charly-distros:fedora` + `/charly-distros:fedora-builder` + `/charly-distros:fedora-nonfree` + `/charly-coder:fedora-coder` + `/charly-distros:charly-fedora` + `/charly-distros:fedora-test` + `/charly-distros:nvidia` |
| **— Agents & skills —** | |
| Sub-agents / dynamic workflows / agent teams / agent-lifecycle or commit-push gate hooks | `/charly-internals:agents` |
| Skill authoring / skill maintenance / where does this doc content belong | `/charly-internals:skills` |

Full index: `plugins/README.md`. Anything not listed: read the index first, load the matching skill second, touch code third.

## The pillars

Each pillar binds a VISION.md tenet to an operational mandate; the named skill owns the detail.

- **Candyboxing.** Secure the box, not the candy: the boundary is a disposable container / VM / check bed with kernel-enforced isolation; inside it, people and agents get the entire toolset. Never secure by whitelisting commands — trust the walls, not the tools. *Detail:* `/charly-internals:disposable`, `/charly-check:check`, `/charly-internals:agents`.
- **Risk Driven Development (RDD).** Prove every high-risk assumption early on a live `disposable: true` bed — docs and code are hypotheses, not proof, when being wrong invalidates the plan; the archetypal unknown is composition at latest versions. A bed that contradicts a doc means the doc is stale — fix it in the same change. The spike (time-boxed, throwaway, one named HOW) never reduces scope, never ships, never replaces R10. *Detail:* `/charly-internals:strict-policy`.
- **Memory hygiene.** A saved memory is a claim: RCA before saving, bed-proof for a high-risk system fact; when memory and the live system disagree, the live system wins — correct or delete the memory in the same change. *Detail:* `/charly-internals:strict-policy`.
- **Agent Driven Evaluation (ADE).** Every candy ships a non-empty `description:` and a `plan:` with at least one deterministic `check:` step — the spec is the test, baked into the image and runnable as acceptance; `charly box validate` enforces it. Step intents: `run:` (state change), `check:` (idempotent probe), `agent-run:` / `agent-check:` (agent steps; an unparseable or timed-out grader fails the step), `include:` (compose another plan). *Detail:* `/charly-check:check` + `/charly-internals:strict-policy`.
- **Schema Driven Design (SDD).** The CUE schema comes before the code; schema-shaped Go is generated (`task cue:gen`), never hand-transcribed — wire types without exception. Regeneration on a clean tree is a no-op; drift is an R1 incident. A generation exception requires an RCA plus a live `cue exp gengotypes` spike, cataloged in `/charly-internals:go` "Generation coverage". *Detail:* `/charly-internals:go` + `/charly-internals:plugin`.
- **Prioritize Clean Architecture Above All Else.** Pick the cleanest long-term approach and remove deprecated code fully — no duplication, no workarounds, dead paths deleted with every reference. Complexity, convenience, and sunk effort never justify weakening the target architecture. *Detail:* `/charly-internals:strict-policy`.
- **The kernel/plugin boundary law.** Core is a kind-blind plugin host: it keeps only generic Envelopes, the three in-core Mechanisms (plugin loading, prescan-dispatch, the wire broker), the single Bootstrap root, and kind-recognition Data. Everything else — every concrete kind's schema, validation, behaviour, and every other kind-blind mechanism — is a plugin candy or sdk kit resolving into a generic envelope. A `spec.<Kind>` field-read, a kind-word switch, or a per-kind map in the kernel is an incomplete seam: a bug with a named K-wave exit, never a kept exception — and difficulty never decides placement. Import-purity (charly/ imports only `github.com/opencharly/spec/*` + the plugin-API wire contract — ZERO `github.com/opencharly/sdk` references, prod, test, and go.mod, CI-enforced by `charly/import_purity_test.go`) and zero-aliases are standing gates. *Detail:* `/charly-internals:plugin` (the boundary law, placement table, and traps).

## Ground Truth Rules (R1–R10)

Engineering discipline (R1–R5) before artifact verification (R6–R9) before the acceptance gate (R10). Operationalization: `/charly-internals:strict-policy`.

- **R1 — RCA every anomaly.** Every failure, warning, or doc-vs-reality divergence triggers `/charly-internals:root-cause-analyzer` before any remediation; "flake", "transient", and blind retry are forbidden framings. A warning is not a pass (R10 needs zero warnings); a documentation divergence is an incident, swept claim-keyed across every sibling doc.
- **R2 — Finish the whole cutover.** Every issue surfaced during an active cutover is fixed: blocking issues in the same tree under this cutover's R10; non-blocking ones join the next thematic batch cutover, planned and begun now. "Pre-existing", "out of scope", and "follow-up PR" are forbidden classifications. Unsure → blocking.
- **R3 — No duplication.** The first time a pattern would land in a second place, refactor to one shared abstraction in the same tree; a fix applies to every surface it logically covers — code, config, candies, probes, docs alike.
- **R4 — No workarounds.** No sleeps, blind retries, magic numbers, environment-specific shims, or manual `podman`/`docker`/`virsh`/`systemctl` commands against charly-managed resources (the `charly` CLI is the only operational interface).
- **R4a — Fix the product first; documentation never routes around a defect.** When a documented behaviour and the code disagree, establish which one holds the INTENT, then **fix the code before touching the prose**. Editing docs to match a bug is forbidden, and so is editing them to avoid one: adding a `git clone`, a `cd`, a `-C` where a flag should have sufficed, an "except on…" caveat, or a "known limitation" note in place of a fix all describe a defect instead of removing it, and they make it permanent by making it look intended. A doc change is legitimate only once the underlying behaviour is right, or when the doc itself was the only thing wrong. **Every command a reader is told to run MUST work with nothing but the `charly` binary installed.** That is the standard, and it is not a documentation nicety — it is the product's purpose. A reader installs one binary and everything the docs show them works: no repository clone, no `cd`, no pre-existing project, no companion tool the page did not name. **Write every page for a reader who installed `charly` as a native package and has no charly checkout anywhere on the machine.** That is the assumed audience, without exception in the body of the docs: no `task` target (those need the repo), no `./bin/charly`, no repo-relative path, no verb supplied by a candy that only exists in this repository. `--repo <owner>/<repo>` reads a published project without a clone; `charly box new project <dir>` starts a local one. The single exception is the INSTALL page, which is where a reader obtains charly and must therefore describe building the package from a source tree — and it leads with the package, scoping the checkout to working ON charly. A page that must name a repository-maintenance command (`task docs:sync`, say) names it as maintenance performed by this project, never as a step the reader runs. Anywhere else, a command that needs more than the binary is a **product defect** — the missing capability is the bug, and the fix belongs in `charly`, never in the prose. If the binary cannot yet do it, that is the thing to fix; documenting the shortfall makes it permanent. If a fix is genuinely out of scope for the current cutover, the finding is filed with its root cause and the doc says nothing rather than something false; a page that is silent is recoverable, a page that documents a workaround teaches it.
- **R5 — Delete legacy completely.** A hard cutover removes the old path and every stale reference, comment, alias, and shim in the same change; afterwards a claim-keyed, per-submodule `git grep` on each removed identifier returns only `CHANGELOG/` history and migration help-text.
- **R6 — Git safety.** Check `git status` and stashes before any destructive working-tree action; never overwrite unrelated changes; no force-push, pushed-history rewrite, hook bypass, or direct push to `main`.
- **R7 — Prove behavior, not compilation.** A green `go test ./...` proves compilation. Runtime-affecting changes run the end-to-end gate — `charly box build` → `charly check box` → deploy to steady state → `charly check live`, or `charly check run <bed>`, which automates the whole sequence — before "done".
- **R8 — Verify emitted artifacts.** Generation changes assert the emitted Containerfile sections and every claimed `ai.opencharly.*` label (`charly box labels`); an empty or missing label is a failure.
- **R9 — Binary equals source.** Build the CalVer-stamped worktree-local binary with `task build:binary` and invoke it through that worktree's `bin/`; verify `charly version`. Runtime OS dependencies belong in `pkg/arch/PKGBUILD`.
- **R10 — Fresh disposable proof.** Verify only on targets explicitly marked `disposable: true`; after committing, re-verify on a fresh `charly update` — pasted output, zero warnings, with check coverage that fails without the change. Run the gate selected by change class (`/charly-check:check` "R10 gate by change class"): documentation-only changes run the non-runtime standards and no bed; code/config run the beds that exercise the change. A dry-run, a rebuild without invocation, a redefined pending gate, or a scope-shrinking flag the user did not name this turn (the flag-override clause) does not count.

Any rule violation forbids commit. Fix it and re-run the full gate, or stop and ask the operator — there is no "downgrade the tier and ship anyway" path.

## Disposable-Only Autonomy

`disposable: true` is the one and only authorization for autonomous destroy + rebuild; it is never inferred from a name, lifecycle tag, or hostname. On a disposable target, unattended `charly update` is the preferred path. On anything else, confirm before destroying — with one standing exception: preempting a declared-`preemptible:` holder is reversible by design and carries standing authorization. *Detail:* `/charly-internals:disposable`.

## Hard Cutover by Default

Every refactor, schema change, rename, or deprecation ships as one phase — one atomic cutover per repo, landing as one commit on `main`. Transitional paths may exist mid-flight but are deleted before the R10 acceptance run, which exercises only the final code. An approved plan is a contract: no mid-execution narrowing, widening, or re-approach — the only legal stops are an unresolvable error or a genuine plan/rulebook/skill contradiction.

Difficulty, size, and priority never reduce or defer scope — they are inputs to finding the HOW (a spike, decomposition into ordered units, delegation to a fresh teammate); running low on context means compact-and-continue or delegate, never stop. Sizing follows the Cutover Sizing Law: a cutover ships the largest coherent scope one R10 gate can honestly prove — small non-blocking fixes batch by theme rather than paying a solo landing ceremony each; too-large contracts decompose into ordered units as forward motion. *Detail:* `/charly-internals:cutover-policy` (the full workflow, forbidden-excuse and forbidden-pattern catalogs, sizing decision procedure).

## Post-Execution Policies

After R10 passes, and only then:

1. Commit — one atomic cutover per repo, Conventional Commits (`!` on removed public surfaces), the attribution trailer at the tier the proof supports.
2. Land via PR — a direct push to `main` is forbidden and mechanically disabled. The author pushes `feat/<slug>`, opens the PR, and never merges its own work; a fresh, independent `pr-validator` re-validates against the rulebook and skills, posts the `charly/pr-validator` status, and only on PASS generates the merge-time CalVer, squash-merges, and tags — the project's authorized, reviewed landing (the merge classifier honors it via the operator's `autoMode.allow` user/managed-settings rule). `--admin` stays forbidden, and **force-push is forbidden on any branch** — `main` only fast-forwards via the validator's squash-merge, and a `feat/` branch, once pushed, advances only by adding commits (amending a pushed branch would require a forbidden force-push). **Amending a `feat/` branch before its first push is a normal authoring action.** On CHANGES-REQUESTED the PR is updated in place.
3. Report — commit hash, tier with proof, PR + validator verdict, pasted R10 outputs.

If R10 fails: root-cause-analyzer first, fix in the same tree, re-run the full R10 fresh. The moment a cutover lands, open the next — non-blocking finds route to their thematic batch; "Phase 2" is not a concept this project has. *Detail:* `/charly-internals:git-workflow`.

## Acceptance checklist

Before declaring completion, every applicable item is YES:

- RDD proved every high-risk assumption early on a disposable bed; every anomaly got RCA before remediation.
- No surviving legacy path, duplication, workaround, or stale doc; per-submodule grep self-test clean.
- Coverage fails without the change; emitted artifacts verified.
- The final-tree R10 change-class gate passed at zero warnings on disposable targets, both outputs pasted.
- The approved plan completed as written; landed as one attributed squash commit per repo via a fresh independent validator, tagged at merge.

## Agents, Workflows & Teams

Substantial or multi-cutover work runs in teammate mode by default: a persistent most-capable-model orchestrator driving cost-scaled teammates and fresh `pr-validator`s, maximum parallelization the default. Every agent definition pins `model: inherit`, so the roster runs whatever model the operating session runs, under any harness — no model is named anywhere in the tooling. The orchestrator owns architectural integrity and independently verifies every teammate decision (bidirectionally — a delegate report is a claim, not proof); teammates own one cutover each; validators alone merge and tag.

Every multi-teammate program is aligned by a binding **north-star document** — concrete end-state, ordered decision heuristics, observed anti-patterns, measured state — named in every spawn brief; on a task-vs-north-star conflict a teammate stops and asks, never resolves locally. This is load-bearing: a wrong brief misdirects every teammate it reaches.

Delegation is fresh context without stopping — spawn a teammate or sub-agent for the next unit rather than halting. A bed run is R10-class wherever it happens (disposable-only, paste-proof survives delegation, no scope-shrinking flags without per-turn authorization); long beds are owned by the persistent session as background tasks. *Detail:* `/charly-internals:agents` (the execution model, responsibility matrix, north-star protocol, bed-scoped parallel testing, lifecycle hygiene).

## Hooks

Hooks enforce deterministic command mechanics only — `.claude/hooks/pre-commit-gate.sh` and `pre-push-gate.sh` (hook bypass, force-push, direct-main push, untokenizable commits, staged lint, alias forms). Attribution truth, change class, CHANGELOG coverage, architecture, and R0–R10 proof are judged by the fresh `pr-validator`, never by hook regexes; there is no reminder layer — rule knowledge lives in this file and the skills. Per-directory CLAUDE.md files are thin signposts naming that area's skills; they restate no rule.

## AI Attribution (Fedora Policy Compliant)

Every AI-authored commit ends with `Assisted-by: <Harness> <Provider Full Model Name> (<confidence>)`, using the exact identity the authoring runtime exposes; matching italicized line on AI-authored issues/PRs. A purely human-authored commit carries no attribution.

| Confidence | Required proof |
|---|---|
| `fully tested and validated` | Every runtime standard + fresh-rebuild R10 on every affected disposable target; changed paths executed live; both outputs pasted |
| `analysed on a live system` | The changed runtime path ran live with retained output; the complete fresh-rebuild R10 did not pass. Never on a dry-run alone |
| `documentation reviewed` | Documentation-only change class (docs, comment-only edits, docs-only gitlink bump) with every non-runtime standard passed. Forbidden the moment code/config behavior is touched |
| `syntax check only` | Compile/unit/validator/dry-run proof only; R10 incomplete — do not commit (runtime classes) |
| `theoretical suggestion` | No validation — never ship |

Confidence never excuses a policy failure: any rule violation forbids commit.

## Key Rules

- The `charly` CLI is the only operational interface for managed resources; a missing verb is a gap to close, never a license for an ad-hoc command.
- Core is a plugin host: every capability — verb, kind, deploy, step, builder, command — is a plugin candy importing only the SDK; in-core capability code is tracked K-wave inventory, never permanent, enforced by the P16 triple gate (the import-surface assertion `charly/import_purity_test.go` — every `charly/` file imports only `spec/*` + the proto/plugin-API contract, zero `github.com/opencharly/sdk`; zero-aliases; per-plugin import hygiene — the former per-file allowlist is retired). *Detail:* `/charly-internals:plugin`.
- Concurrency is proven under high load (the disposable roster at maximum parallelism); surfaced issues are root-cause fixed, never serialized-to-hide or retried away. *Detail:* `/charly-internals:agents`.
- One entity kind `candy:` (`base:`/`from:` marks an image), one filename `charly.yml`, lowercase-hyphenated globally-unique top-level names, shape-based routing, Go-style `import:` namespaces; residual legacy `include:` is a hard load error pointing at `charly migrate`. *Detail:* `/charly-image:image`, `/charly-image:layer`.
- Init-system polymorphism via mixed `service:` entries — never `<name>-host`/`<name>-pod` sibling candies.
- Per-kind CalVer `version:` is authoritative identity; resolution is per-entity post-fetch (`charly box reconcile` aligns pins); `ai.opencharly.*` capabilities are an OCI-label contract with content-derived EffectiveVersion.
- Deploy substrates `local:`/`vm:`/`k8s:`/`android:`/`pod:`/`group:` all consume the shared InstallPlan IR (no `host:` venue kind); deploy fetches nothing speculative; sibling members probe each other via `${HOST:<member>}`. *Detail:* `/charly-core:deploy`, `/charly-internals:install-plan`.

The named skills own the full technical rules; this index never expands into a second copy of them.

## Where things are documented

- **Rules & mandates** → this file + `AGENTS.md` (equivalent policy).
- **Features & command reference** → `README.md`.
- **Usage & architecture** → skills (`plugins/README.md` is the index) — the single source of truth for *how*.
- **Thesis & direction** → `VISION.md`.
- **Public site** → [opencharly.ai](https://opencharly.ai), built from the `docs/` submodule: a small hand-authored narrative plus a reference/recipe catalog GENERATED from the sources above by `charly docs generate`. Never hand-edit a generated page; fix the source and run `task docs:sync`.
- **History** → each repo's `CHANGELOG/` (one file per CalVer release, `<YYYY.DDD.HHMM>.md`, shared with the release tag) — the sanctioned historical context named by R5's grep self-test; everything else states current behavior in present tense.
