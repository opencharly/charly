# OpenCharly — The Candy Factory for You and Your Agents

Secure the box, then fill it with the whole candy store: compose, build, deploy, and manage **boxes** (container images) from a library of fully configurable **candies**, driven by the `charly` Go CLI — built for you *and* your agents, on Docker, Podman, QEMU, or libvirt.

This file is the project's rulebook — rules and mandates ONLY.

**Part I — Dispatch: load skills before anything.**

## R0. SKILLS FIRST — THE SUPREME RULE

**R0's supremacy is total within its domain.** R0 dictates WHEN the skill loads (first, always), never WHETHER its claims are true — so it cannot conflict with R1–R10 or RDD, and any apparent conflict resolves the same way: load the skill, then proceed under the other rule. If you feel the impulse to act "just this once" without the skill, that impulse IS the violation.

Before you read a single line of source, run a single `charly` / `bash` / `grep` command, launch a single Agent, or edit a single file — **invoke the matching skill(s) via the `Skill` tool**, ALL of them in ONE message when several rows match (partial loading is full failure). The **Skill Dispatcher** below maps triggers → skills. Any action taken without the matching skill loaded is a **protocol violation**, regardless of whether the action was technically correct; correct course the moment you catch yourself: STOP, invoke the skill(s), then proceed.

**Consult order (absolute):** `skills → CLAUDE.md → memory → code exploration (last resort)`. This orders where you LOOK FIRST, not what is TRUE. For a HIGH-RISK claim, a live `disposable: true` bed outranks every document on this list (see **Risk Driven Development (RDD)**): the skill is the mandatory first hypothesis, never the final verdict. Running a bed AFTER loading the skill is RDD compliance; running anything INSTEAD of loading the skill is the R0 violation.

## Skill Dispatcher

Consult this table BEFORE the first tool call of every task; when several rows match, load ALL their skills in ONE message (parallel `Skill` calls).

| Trigger (what the user said or what you're about to do) | Skills to load BEFORE doing anything |
|---|---|
| **— Build & author boxes and candies —** | |
| Editing a candy (`candy/<name>/charly.yml`), candy authoring, candy tasks/services | `/charly-image:layer` |
| Editing a box (`box/<name>/charly.yml` — boxes live in the `box/<distro>` submodules; main owns none), box composition | `/charly-image:image` |
| Authoring a plugin (a candy with a `plugin:` block) / builtin vs out-of-tree plugin / per-plugin `.cue` schema (single source → `gengotypes` for dev + schema-over-`Describe` RPC at runtime) / the plugin SDK / `charly/plugin/**` / a compiled-in plugin candy (`compiled_plugins:`) or host-coupled kit candy / an external plugin module | `/charly-internals:plugin` |
| `charly box build` / `charly box generate` / Containerfile | `/charly-build:build` + `/charly-build:generate` + `/charly-internals:generate-source` |
| `charly box validate` / schema error | `/charly-build:validate` |
| `charly migrate` / schema migration / legacy → latest CalVer / CalVer schema version | `/charly-build:migrate` |
| `charly box reconcile` / cross-repo `@github` pin alignment / candy-version-mismatch cleanup | `/charly-build:reconcile` |
| Secret management / `charly secrets` / Secret Service / GPG `.secrets` | `/charly-build:secrets` |
| `charly clean` / build-artifact retention / `keep_images` / `keep_check_runs` / image-tag pruning / `.check` run cleanup | `/charly-core:clean` |
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
| Git/`gh` workflow — `feat/` branch, commit, push, ff-merge to main, tag, worktree, sync-to-upstream, branch/worktree prune, PR create, `gh` approve/merge, cross-repo R10 landing | `/charly-internals:git-workflow` |
| **— Discipline & process —** | |
| Hard-cutover concerns / rename sweeps | `/charly-internals:cutover-policy` |
| Engineering-discipline triggers (failure surfaced / dup pattern / ad-hoc fix tempting / "out of scope" framing) | `/charly-internals:strict-policy` |
| Unexpected failure / error / anomaly | `/charly-internals:root-cause-analyzer` agent (BEFORE any fix) |
| **— Go & internals —** | |
| Go source work (adding/modifying `charly` commands) | `/charly-internals:go` |
| Go code-quality / CLAUDE.md-compliance audit / `golangci-lint` / `dupl` / duplication or dead-code check / `.golangci.yml` | `/charly-internals:go-quality` + `/charly-internals:strict-policy` |
| IR / InstallPlan / DeployTarget / OCITarget | `/charly-internals:install-plan` |
| OCI labels / capabilities contract | `/charly-internals:capabilities` |
| Egress config validation — validating/generating the config files charly WRITES to a system (`charly/egress.go`, `ValidateEgress`, vendored CUE schemas under `schema/vendor/`, the `task cue:vendor` pipeline, cloud-init/k8s/units/ssh_config/libvirt-XML egress) | `/charly-internals:egress` |
| VmSpec / libvirt / cloud-init / OVMF internals | `/charly-internals:vm-spec` (+ renderer skills as needed) |
| **— Orientation: "what does candy X do?" / "what's in box X?" —** | |
| Pod apps, language runtimes, infrastructure services, CLI utilities / the `charly` binary | `/charly-<family>:<name>` — families: `jupyter`, `coder`, `selkies`, `openclaw`, `versa`, `ollama`, `openwebui`, `comfyui`, `immich`, `hermes`, `filebrowser` (pod apps); `languages` (python, python-ml, pixi); `infrastructure` (postgresql, redis, k3s, traefik, supervisord, tailscale, gocryptfs, virtualization, dbus-layer, tmux-layer, …); `tools` (ripgrep, himalaya, whisper, charly, …) |
| Base distros / GPU runtime | `/charly-distros:<name>` (arch, fedora, debian, ubuntu, cachyos, nvidia, cuda, rocm, …) |
| CachyOS images / `cachyos*` / `charly-cachyos` workstation profile / `box/cachyos` submodule | `/charly-distros:cachyos` + `/charly-vm:cachyos` + `/charly-local:charly-cachyos` |
| Debian images / `debian*` / `box/debian` submodule | `/charly-distros:debian` + `/charly-distros:debian-builder` + `/charly-distros:debian-debootstrap` + `/charly-coder:debian-coder` + `/charly-vm:debian` |
| Ubuntu images / `ubuntu*` / `box/ubuntu` submodule | `/charly-distros:ubuntu` + `/charly-distros:ubuntu-builder` + `/charly-distros:ubuntu-debootstrap` + `/charly-coder:ubuntu-coder` + `/charly-vm:ubuntu` |
| Fedora images / `fedora*` / `box/fedora` submodule (incl. the GPU base `nvidia` / `python-ml` + `sway-browser-vnc`) | `/charly-distros:fedora` + `/charly-distros:fedora-builder` + `/charly-distros:fedora-nonfree` + `/charly-coder:fedora-coder` + `/charly-distros:charly-fedora` + `/charly-distros:fedora-test` + `/charly-distros:nvidia` |
| **— Agents & skills —** | |
| Sub-agents / dynamic workflows / agent teams / agent-lifecycle or commit-push gate hooks | `/charly-internals:agents` |
| Skill authoring / skill maintenance / where does this doc content belong | `/charly-internals:skills` |

Full index: `plugins/README.md`. This table covers the top triggers; anything not listed requires reading the index FIRST, loading the matching skill SECOND, touching code THIRD. Never reverse this order. This table is the SOLE copy — it is deliberately mirrored nowhere.

**Part II — Vision: the tenets this file enforces.**

## Enforcing VISION.md

CLAUDE.md enforces `VISION.md`: every tenet binds to an operational mandate here and an owning skill. VISION states the *why*; the bound section states the *rule*; the skill owns the *how*.

## Candyboxing

OpenCharly secures the BOX, not the candy: the boundary is a disposable container / VM / check bed with kernel-enforced isolation (rootless podman + user namespaces, libvirt `qemu:///session` VMs, gocryptfs volumes, tailscale-scoped networking), and inside it the agent gets the ENTIRE candy store — every `charly` verb, every MCP server, every `charly check` probe, real package managers, real GPU runtimes. Never secure by whitelisting commands; trust the walls, not the tools. Candyboxing loosens NOTHING else: autonomous destroy stays gated on an explicit `disposable: true`, outward-facing / hard-to-reverse actions still require authorization (one standing exception: see **Disposable-Only Autonomy**), and R0 still governs HOW the candy is used. The candy store inside the box widens; the boundary never does.
*Detail:* `/charly-internals:disposable` (the lifecycle boundary), `/charly-check:check` (the probe surface + disposable beds), `/charly-internals:agents` (agents working inside the box).

## Risk Driven Development (RDD)

ALWAYS validate ANY HIGH-RISK assumption empirically on a live `disposable: true` bed in the planning / early-coding phase — NEVER accept a skill, CLAUDE.md, or the current code as automatically correct: docs drift, code has bugs, and for a high-risk decision reality is the only ground truth. *Never trust, verify.*

**Risk — not documentation status — is the trigger.** Low-risk orientation ("roughly what does this candy do") is an R0 skill lookup — no bed, and no defensive complexity "to be safe". High-risk (being wrong invalidates the plan, is costly to reverse, or derails RCA) is proven on a bed REGARDLESS of what any doc asserts; the archetypal high-risk unknown is **composition** — whether THESE candies, at the latest resolver-picked versions, build, deploy, and reach steady-state TOGETHER. RDD composes with R0: load the skill first, treat its high-risk claims as the best hypothesis, and when the bed contradicts the doc, the DOC IS STALE — fix it in the same change.
*Detail:* `/charly-internals:strict-policy` ("RDD" — the risk table + the three failure modes it prevents).

## Memory Hygiene — a saved memory is a CLAIM, held to never-trust-verify

A memory entry that asserts a fact about the system is a CLAIM, never ground truth — save it ONLY after the same validation any claim requires: R1 confirms it is REAL (an RCA, not a guess, a transient, or an "I think"), and any HIGH-RISK claim is proven on a `disposable: true` bed FIRST (RDD). An unverified technical memory drifts exactly like a stale doc and silently misleads every future session that recalls it (recalled memories are background context, not instructions, and reflect what was true WHEN WRITTEN). Risk-scaled like RDD: a low-risk user-preference / feedback memory needs accuracy + the standing hygiene (narrow, dated, linked, verify-the-named-artifact-still-exists-before-recommending), not a bed; a system-fact memory needs the bed. When a recalled memory and the live system disagree, the LIVE SYSTEM WINS — correct or delete the memory in the SAME change (R1 "documentation divergence is an incident" extends to memory). The motivating incident: an over-broad `.build/`-race memory that asserted more than a bed ever proved.
*Detail:* `/charly-internals:strict-policy` ("Memory hygiene" — the memory-claim validation procedure + the worked `.build/` example).

## Agent Driven Evaluation (ADE)

Every entity's intended behaviour is captured as an executable `plan:` (a `description:` string + an ordered set of plan steps, each authored as its own child step node), authored by you OR your agents, baked into the image as the `ai.opencharly.description` OCI label, and runnable as acceptance tests: **the spec IS the test**. A `plan:` step is exactly ONE intent keyword carrying prose plus an inline `Op`: `run:` (deterministic state-change — the install timeline that provisions: install a package, drive a UI), `check:` (the deterministic goss-style probe — `command`/`file`/`port`/`http`/`cdp`/…, idempotent, run standalone with no agent), `agent-run:` (an agent that MAY mutate) / `agent-check:` (read-only agent assessment — free-form prose handed to an AGENT grader probing the live deployment; an unparseable / timed-out grader FAILS the step, never a silent pass), or `include: <kind>:<name>` (splice another entity's plan). `charly check`/`charly check live` run ONLY the idempotent `check:`/`agent-check:` steps. The plan lives on the CANDY that provides the behaviour, so ONE plan covers every box composing it (R3). ADE is MANDATORY for every candy: each candy MUST ship a non-empty `description:` string AND a `plan:` with **at least one deterministic `check:` step** — `charly box validate` hard-errors otherwise — and the baked plan runs and must pass. RDD proves the assumptions, ADE specifies and grades the behaviour, R10 proves the fresh rebuild — three points on one *never trust, verify* arc.
*Detail:* `/charly-check:check` ("Agent Driven Evaluation (ADE)" — the Specify → Bind → Run → Iterate → Bake → Gate loop and commands) + `/charly-internals:strict-policy` ("ADE").

## Prioritize Clean Architecture Above All Else

Always pick the cleanest long-term approach and prioritize a clean codebase with all deprecated code fully removed above everything. You have all the time in the world; getting things properly done is ALWAYS worth the effort. Architecturally this binds the same norms as R3–R5: no duplication on first surface, generic over ad-hoc, no workarounds, dead paths deleted with every reference.
*Detail:* `/charly-internals:strict-policy` (forbidden-pattern catalog + worked examples).

**Part III — Ground Truth Rules: the hard gates.**

## Ground Truth Rules — NEVER claim success without these (HARD RULES)

Engineering discipline (R1–R5) comes BEFORE artifact verification (R6–R9) BEFORE the final acceptance gate (R10) — in that order, no exceptions. A violation of ANY rule forbids commit (see **AI Attribution**). R1–R5 are operationalized in `/charly-internals:strict-policy`.

- **R1. Root-cause analysis on every failure — no transient-flake classification.** Every failure, error, warning, or anomaly surfaced by ANY tool (build, test, validator, runtime, check, deploy, lint, hook) — or a divergence between any documentation, skill, or code comment and observed reality, discovered by ANY means (a bed, a code reading, an agent, a human report) — triggers `/charly-internals:root-cause-analyzer` BEFORE any remediation; "probably a flake" / "rerun and see" / "transient" / "environmental" are FORBIDDEN framings, blind retry is itself a violation, and a genuinely-external root cause is documented with evidence, never assumed. **A warning is not a pass:** R10 succeeds only at ZERO warnings — every warning gets the analyzer, then a real fix (a version-mismatch warning: `charly box reconcile`). **Documentation divergence is an incident:** its RCA sweeps EVERY other doc/skill/comment carrying the same false/outdated/misleading claim — not just the file where it surfaced (claim-keyed, R5) — and the fix for the changed surface and its sibling-set is BLOCKING in the current cutover (R2). *Scope:* the FIRST occurrence, always — no second-occurrence threshold. *Detail:* `/charly-internals:strict-policy` (R1).
- **R2. No "pre-existing" / "out of scope" / "unrelated" / "follow-up PR" classifications.** Every issue surfaced during the active cutover is fixed: a BLOCKING issue (the change is incorrect, incomplete, or unsafe without it) in the SAME working tree under this cutover's R10; a NON-BLOCKING issue (this cutover's own R10 passes and proves its claim WITHOUT the fix) as its OWN immediate-next cutover the moment this one lands — never an indefinite "follow-up / someday". Unsure → blocking. **A known divergence is blocking by R2's own test** — a tree carrying a false claim is not "correct" — so the divergence on this cutover's surface and its sibling-set is fixed here; a genuinely-unrelated divergence is RCA'd immediately too, but as its own immediate-next cutover. Mislabeling to ship faster is the forbidden split; escalate only at a genuine crossroad you cannot resolve alone. *Scope:* everything surfaced while a cutover is open — failing tests, warnings, crashes, dead references, stale docs. *Detail:* `/charly-internals:strict-policy` (R2 — the separability test + escalation path).
- **R3. No code duplication; generic, reusable solutions over ad-hoc patches.** The FIRST time the same pattern, predicate, filter, transform, or guard would land in a second place, refactor to ONE shared abstraction in the SAME working tree; every fix MUST apply cleanly to ALL surfaces it logically covers, never just the one that prompted the report. *Scope:* code, config, candies (sibling `<name>-host`/`<name>-pod` naming is FORBIDDEN), check probes, docs. *Detail:* `/charly-internals:strict-policy` (R3 — forbidden patterns + worked examples).
- **R4. No ad-hoc workarounds.** Sleep loops, retry-on-flake, magic-number tuning, environment-specific shims, "works on my machine" fixes, and ad-hoc `podman` / `docker` / `virsh` / `systemctl` commands against charly-managed resources (the `charly` CLI is the ONLY operational interface) are FORBIDDEN: a race is fixed with a synchronization primitive, never a delay; a magic value is named, config-sourced, and validated on load; a fix that works on one machine only is a bug report, not a fix. *Scope:* all code and config, including tests, hooks, and check beds. *Detail:* `/charly-internals:strict-policy` (R4 — forbidden patterns + authorized replacements).
- **R5. Hard cutover: the deprecated path AND every stale reference deleted in the same change.** The SAME commit deletes the old code path, every comment / TODO / DEPRECATED marker on it, and every reference, docstring, error message, skill paragraph, test fixture, or hook string naming a deleted identifier; afterwards `git grep '<deleted-id>'` returns ONLY the repo's `CHANGELOG/` history files / migration help-text. The acceptance test: rebuild from the new config, run it to steady state, AND pass the grep self-test — all of it AGAINST the final code, with every deprecated / transitional / dual-mode path deleted BEFORE the R10 acceptance run (never after a green R10, which would verify a state that will not ship); deleting the old file while the new path silently drops a stage is a masked regression, not a cutover. **The sweep is claim-keyed, not only identifier-keyed:** a false/outdated/misleading claim is swept across EVERY doc/skill/comment that repeats it, even when no identifier was deleted (R1 makes the divergence an incident; the grep self-test is one instrument of the broader claim sweep). *Scope:* every rename, schema change, or deprecation. *Detail:* `/charly-internals:strict-policy` (R5) + `/charly-internals:cutover-policy`.
- **R6. Check git status + stashes before destructive working-tree actions.** `git stash` discards in-progress work and `rm` on a tracked file is destructive; when the sandbox blocks an action, read the reason and find a non-destructive alternative — never work around it with a cleverer command. *Scope:* any `rm` / `stash` / `checkout` / `reset` touching tracked or in-progress state. *Detail:* `/charly-internals:git-workflow` (invariants).
- **R7. Unit tests never substitute for runtime verification — mandatory end-to-end gate.** A green `go test ./...` proves compilation, not behaviour: any change that can affect Containerfile generation, OCI labels, init systems, service startup, or deploy code runs `charly box build` → `charly check box` → `charly start`/`charly update` to `active (running)` → `charly check live` BEFORE "done"; any failure invokes R1. `charly check run <bed>` automates that whole sequence on a disposable bed and is the canonical R10 gate for runtime classes — pick the bed whose kind matches the change; `charly check run` on an `iterate:` bed is the multi-hour AI benchmark, never a quick gate. *Scope:* before "done" on every runtime-affecting change. *Detail:* `/charly-check:check` ("Three primary modes", "Exit codes", "The 10 Testing Standards").
- **R8. Generated-artifact invariants — Containerfile sections AND OCI labels verified.** When a refactor touches generation, assert every critical section in the emitted Containerfile and, after `charly box build`, verify every claimed capability label via `charly box labels <ref> --format <key>`; the emitted artifact is the source of truth, and an empty or missing label is a FAILURE, never a warning. *Scope:* anything that can change `.build/<image>/Containerfile` or an `ai.opencharly.*` label. *Detail:* `/charly-build:generate` + `/charly-internals:capabilities`.
- **R9. Deployed binary matches source AND runtime deps are declared in package management.** Syncing source does not rebuild the binary — after pushing code, rebuild on the target and verify `charly version`, or the fix under test isn't under test; every runtime OS dependency goes into `pkg/arch/PKGBUILD` `depends=` (the single source of truth) — a manual install on one host is a bug report disguised as a fix. *Scope:* every change exercised on a remote or disposable target. *Detail:* `/charly-internals:go` (R9) + `/charly-check:check` (Standards 7–8).
- **R10. Verify on a `disposable: true` target; prove it on a fresh rebuild.** Test ONLY on targets explicitly marked `disposable: true` (none suitable → create one first; never assume disposability from a name, lifecycle tag, or hostname); if a test breaks the target, `charly update` it back to committed config before anything else; after committing the fix, re-verify on a FRESH `charly update` — pasted fresh-rebuild output, at ZERO warnings, with the check/test coverage that proves the new functionality, is the acceptance gate.
  *Fraud clauses — each a hard violation (motivating incidents: the repo's `CHANGELOG/`):*
  - **A `--dry-run` does NOT count.** R10 means every new or changed code path executed LIVE, with pasted output for each changed piece.
  - **A rebuild alone does NOT count.** The rebuild is preflight; if the changed runner / loop / verb never executed against it, the honest tier is `syntax check only` — and committing at that tier is itself a violation: STOP and ask.
  - **Task-editing fraud is FORBIDDEN.** R10 has ONE definition: no redefining, downgrading, or deleting a pending R10 task; multi-hour runs ARE the work; session budget NEVER downgrades R10.
  - **Flag overrides require explicit user authorization in the SAME turn.** The `iterate:`/bed config in the `check:` block IS the test specification; passing ANY scope-shrinking `charly check run`/`live` flag without the user naming the flag THIS turn is the same fraud class (authoritative catalog: `/charly-check:check` "Flag discipline"); "to fit session bounds" is a confession, not a defence.
  *The gate by change class — run the gate that EXERCISES the change: a gate that cannot fail on the change proves nothing (waste), a change whose gate never executed is unproven (fraud). Authoritative matrix: `/charly-check:check` "R10 gate by change class".*
  - **Documentation-only change class** (`*.md`, code comments, or a submodule pointer bump to an all-documentation submodule commit — zero behavior change): NO bed run, NO build — the gate is the non-runtime standards (adversarial consistency review, R5 grep self-test, cross-reference validation, markdown integrity, the PreToolUse gates), and it earns the `documentation reviewed` attribution tier (see **AI Attribution**), never a runtime tier. Running check beds on prose is waste, not diligence.
  - **Code / config / scripts**: the matrix row that exercises the change — `charly` Go code: `go test ./...` + `task build:charly` (R9) + `charly check run <bed>` for EACH bed whose kind matches a touched code path (cross-cutting loader/resolver/IR changes: run every matching bed — fan the roster out CONCURRENTLY, one agent per `charly check run <bed>`, via the `/verify-beds` workflow; in-spec for that class); candy / box / deploy config: `charly box validate` + build + the bed (or deploy) that COMPOSES the changed entity, through the fresh-`charly update` gate; hook / workflow scripts: parse + execute the changed script live. *Scope:* every change, before "done" and before any commit, at its class gate — run the **Acceptance checklist** below. *Detail:* `/charly-internals:disposable` ("What counts as an R10 run") + `/charly-check:check` ("R10 gate by change class", "Flag discipline").

**Part IV — Process: how a change lands.**

## Disposable-Only Autonomy

**`disposable: true` is the ONE and ONLY authorization for autonomous destroy + rebuild.** Default `false`, explicit opt-in only: no derivation from other fields, no "looks like a test bed" heuristic, no hostname assumptions — explicit-only is what makes autonomy safe on shared infrastructure with live users on other resources.

- On a disposable target, unattended `charly update <name>` is the PREFERRED path — hesitating to rebuild when verification demands it is the opposite failure mode, and the one that produces claimed-but-unverified fixes.
- On any other resource, confirm with the user before any destroy (an irreversible teardown). **Standing exception:** preempting a declared-`preemptible:` holder is reversible by design (graceful stop + crash-safe `restore: always`) and carries STANDING authorization — preempt autonomously, no per-run confirmation.

*Detail:* `/charly-internals:disposable` (flag semantics, the ephemeral/preemptible axes, what counts as an R10 run) + `/charly-check:check` ("The 10 Testing Standards").

## Hard Cutover by Default — ONE PHASE, test EVERYTHING at the end

**Every refactor, schema change, API rename, or deprecation ships as ONE PHASE — no intermediate coexistence, no phased rollout, no splitting across conversation turns.** Split into TASKS, not phases: a 15-task cutover is still ONE phase, ONE atomic commit per repo; marking a task `completed` is a TODO signal, never a commit signal. Only the COMMIT is gated — on R10 against the FINAL code — never the act of verifying: run `charly` to verify at ANY stage, as often as useful; transitional aliases, legacy-accepting paths, and dual-mode dispatch are permitted DURING mid-flight verification but MUST be deleted BEFORE the R10 acceptance run — the fresh-rebuild gate whose pasted output authorizes the commit runs ONLY against the FINAL, transitional-free code, so running it with ANY transitional / legacy / deprecated / dual-mode / backcompat path still present tests a state that will not ship and does NOT count. Plans are authored full-scope regardless of estimated time or context, and an approved plan is a CONTRACT — fixed once approved and NEVER changed mid-execution: not narrowed, not widened, not re-approached, not silently downgraded; the ONLY legal response to a needed change is to STOP and ask the user (one of the two valid stops below), never a unilateral mid-flight edit of the contract. The ONLY valid stops, at any stage: (a) an error you cannot resolve without user input; (b) the plan contradicts itself, CLAUDE.md, or a loaded skill — STOP and ask; never silently downgrade scope or commit a partial state.

*Detail:* `/charly-internals:cutover-policy` (workflow, the forbidden-pattern catalog, required deliverables, rationale) + `/charly-build:migrate` (the single idempotent `charly migrate`).

## Post-Execution Policies — what happens AFTER R10 passes

### After R10 passes (and only after)

1. **Commit** — ONE atomic commit per repo covering the entire cutover (multiple commits FORBIDDEN); Conventional Commits with the `!` marker when a public surface is removed.
2. **AI-attribution trailer** — `Assisted-by: Claude (<confidence>)` at the tier the proof supports, never inflated (see **AI Attribution**).
3. **Auto-land** — the R10 pass is the SOLE landing trigger: push `feat/<slug>`, `--ff-only` merge into `main` (if `main` advanced: re-sync, rebase, re-run R10), tag the new `main` HEAD `v<CalVer>`, push `--follow-tags`, delete `feat/` local + remote. **NEVER force-push** — no `--force`, no `--force-with-lease`, on any branch in any repo, ever; `main` only fast-forwards, tags are immutable add-only.
4. **Report** — commit subject + hash, confidence tier with its proof, what was pushed, pasted R10 outputs (exploratory + fresh-rebuild).

**If R10 fails:** run `/charly-internals:root-cause-analyzer` BEFORE any fix (blind retry FORBIDDEN); fix in the SAME working tree (never a follow-up PR); re-run the FULL R10 from a fresh `charly update`, not just the failing piece; commit only on an end-to-end pass of the FINAL code.

**What is NOT post-execution:** folding new work INTO the current cutover is FORBIDDEN — but STARTING the next one is your job: the moment this cutover lands, you automatically open the next cutover for ANY issue you have found (backports and cherry-picks included), each its own atomic, fully-R10'd change, pausing to ask ONLY at a genuine crossroad you cannot resolve from the request, the code, the loaded skills, or sensible defaults. "Phase 2" is a forbidden concept.
*Detail:* `/charly-internals:git-workflow` (CalVer tag computation, multi-repo dependency order, fork+PR path, pruning, the report format).

### Acceptance checklist

Before declaring the turn done — this single checklist merges end-of-turn verification with the landing gate. Every YES:

**Discipline & verification**
- [ ] RDD: every HIGH-RISK assumption proven EARLY on a `disposable: true` bed — above all composition-at-latest-versions — none carried on a doc/code reading alone?
- [ ] `/charly-internals:root-cause-analyzer` ran on every failure / warning / anomaly observed (R1)?
- [ ] Every issue surfaced during the session fixed in this cutover or explicitly escalated (R2)?
- [ ] `git grep` on every removed identifier returns ONLY the repo's `CHANGELOG/` history-file / migration-help-text context (R5)?
- [ ] Real artifact built from the changed source on the target host; deployed binary's version matches; every runtime dep via package management (R9)?
- [ ] Feature exercised end-to-end on the live target — ONLY on targets explicitly marked `disposable: true`, any target broken during exploration `charly update`d back to clean (R10)?
- [ ] The change ships the check/test coverage that PROVES its functionality and R10 exercised it — a change whose new functionality has no test that would FAIL without it is not landable (check-coverage gate)?
- [ ] Every transitional alias / legacy-accepting path / dual-mode dispatch / backcompat shim DELETED before the R10 acceptance run — R10 exercised the FINAL, transitional-free code (R5 / Hard Cutover)?

**Acceptance gate**
- [ ] R10's change-class gate ran AGAINST THE FINAL CODE (not an intermediate state — no transitional / legacy / deprecated / dual-mode path present) — on every affected disposable target for code/config classes, via the non-runtime standards for docs-only?
- [ ] (code/config classes) Both exploratory and fresh-rebuild R10 outputs pasted; post-action state of every target healthy (running, not paused, not crashed)?
- [ ] ZERO warnings remain (zero-warnings gate — per R1, a surviving warning is an R10 failure, never an accepted end state)?

**Landing**
- [ ] ONE atomic commit per repo (on the `feat/<slug>` branch), with the AI-attribution trailer at the tier the proof supports (no inflation)?
- [ ] Auto-landed on R10 PASS: `feat/` ff-merged into `main`, `main` HEAD tagged `v<CalVer>`, pushed, `feat/` deleted — NO force-push anywhere; `git status` clean afterward (stray artifacts are their own immediate-next cutover)?
- [ ] The approved plan executed AS WRITTEN — no mid-execution scope change (narrowed / widened / re-approached) except via a STOP-and-ask (plan = CONTRACT)?
- [ ] No "Phase 2 / TODO / will do next time" deferred work surfaced in this plan?

**Part V — Agents & Attribution.**

## Agents, Workflows & Teams

OpenCharly is driven from Claude Code's multi-agent primitives — **sub-agents** (`plugins/internals/agents/*.md`), **dynamic workflows** (`.claude/workflows/*.js`, run `/<name>`), and **agent teams** (experimental, enabled in the committed `.claude/settings.json`). **Full reference: `/charly-internals:agents`.** The brief:

- **Prefer agents over background tasks** — everything that CAN run as an addressable, operator-visible sub-agent or teammate SHOULD; a background `Workflow` is a LAST RESORT for control flow a team can't express. The one exception: long-running work that outlives a turn (a VM/emulator bed) runs as a harness-tracked background task owned by the persistent session.
- **Agent roster & workflows** — *executors* return verbatim proof: `check-bed-runner` (full `charly check run <bed>`), `deploy-verifier` (read-only). *Enforcers* gate claims: `root-cause-analyzer` (R1), `testing-validator` (proof-before-"works"), `layer-validator` (pre-edit `charly.yml`). Workflows: `/verify-beds [bed …]` fans the disposable check beds out as the R10 gate; `/audit-deploy-configs [target …]` evaluates deploy configs.
- **Binding rule — running a bed is R10-class.** Disposable-only authorization; the commit is gated on a full final-code live test that is pasted (beds run freely throughout to verify); no scope-shrinking flags without per-turn authorization; **paste-proof survives delegation** — the executor returns the verbatim verdict + exit code and the delegating agent PASTES it.
- **Hooks doctrine.** Hooks are POINTERS to this file + skills — the prompt-submit reminder names the full R0–R10 + RDD + ADE roster as second-pass *triggers* (never copies of the rule bodies — duplication drifts; CLAUDE.md is the single current source) — plus deterministic `PreToolUse` gates that block only unambiguous invariants: hook bypass via `--no-verify` or a `core.hooksPath` override (on `git commit` AND `git push`), an illegal/absent attribution tier, force-push, and a runtime-tier commit staging no `CHANGELOG/<YYYY.DDD.HHMM>.md` entry in a CHANGELOG-tracking repo. Hooks gate mechanical invariants; agents judge proof. Never re-bloat the reminders into rule-body copies.
- **Per-directory CLAUDE.md signposts.** This root file is the single canonical R0–R10 rule-set; per-directory CLAUDE.md files (`charly/`, `candy/`, `plugins/`, each `box/<distro>`) are THIN signposts naming that area's skills — they restate no rule. *Detail:* `/charly-internals:skills`.

## AI Attribution (Fedora Policy Compliant)

Per the [Fedora AI Contribution Policy](https://docs.fedoraproject.org/en-US/council/policy/ai-contribution-policy/), **every commit Claude is involved in — in ANY way — MUST carry an `Assisted-by: Claude (<confidence>)` trailer** (if Claude touched it at all, the classification is correct; attribute at the tier the proof supports, and when unsure whether the work was "AI enough", attribute); every such GitHub issue/PR ends with `*Assisted-by: Claude (<confidence>)*`. A purely **human-authored** commit with ZERO Claude involvement carries **no** AI attribution — it does not pass through Claude's PreToolUse commit gate, so nothing is imposed on it.

| Confidence | When to use |
|-----------|-------------|
| `fully tested and validated` | *(runtime classes)* All 10 Testing Standards met + fresh-rebuild R10 on every affected `disposable: true` target + the cutover's NEW/CHANGED code paths actually executed against the fresh rebuild + both R10 outputs pasted |
| `analysed on a live system` | *(runtime classes)* A live invocation of the changed runner / loop / verb actually ran AND its output is pasted. A rebuild WITHOUT the subsequent invocation does NOT qualify; NEVER this tier on a `--dry-run` alone |
| `documentation reviewed` | *(the Documentation-only change class)* The change touches ONLY documentation — `*.md`, comment-only code edits, or a submodule pointer bump to an all-documentation submodule commit, ZERO behavior change — and ALL non-runtime standards passed (adversarial consistency review, R5 grep self-test, cross-reference validation, markdown integrity, the `pre-commit-gate.sh` / `pre-push-gate.sh` gates). No runtime verification exists to run, so the runtime tiers do not apply. FORBIDDEN the moment ANY code/config behavior is touched — that surface takes a runtime tier and the docs ride along |
| `syntax check only` | *(runtime classes)* Compile / unit tests / validators / dry-run passed; the live runner did NOT execute. The honest default when a runtime R10 hasn't run — pair with explicit "R10 not yet run" AND do NOT commit (pairing this tier with a commit is a violation; STOP and ask) |
| `theoretical suggestion` | No validation performed — FORBIDDEN as a shipped-code tier |

**`documentation reviewed` is the Documentation-only change class's honest tier** — a docs/policy-only cutover (`*.md` files, comment-only code edits, or a submodule pointer bump to an all-documentation submodule commit, ZERO behavior change — no behavioral Go / YAML-schema / box/candy-config edit, no other runtime surface) has no R10 bed; it earns `documentation reviewed` when ALL non-runtime standards pass, and the `syntax check only → do NOT commit` clause (a runtime-class rule) does not apply to it. The runtime tiers do not apply to prose, and `documentation reviewed` is conversely FORBIDDEN the moment a cutover ALSO touches code or config — that surface's R10 gates it at a runtime tier and the docs ride along in the same commit. `pre-commit-gate.sh` enforces the boundary: it rejects `documentation reviewed` on any commit whose staged diff is not all-documentation — a staged submodule pointer bump counts as documentation only when the bumped submodule commit's own diff is itself all-documentation (a bump integrating submodule code is rejected, taking a runtime tier). See R10 "Documentation-only change class"; the full class → gate → tier cross-walk is `/charly-check:check` "R10 gate by change class".

**Any rule violation forbids commit. Period.** A violation of R1–R10 or **Prioritize Clean Architecture Above All Else** means NO commit, at any tier, in any repo, with any wording — there is no "downgrade tier and ship anyway" path. The only authorized responses: (a) fix the violation in the same working tree and re-run all verification, or (b) escalate to the operator and STOP. Suggesting any other path is itself a violation. Worked commit-message example: `/charly-internals:git-workflow`.

**Part VI — Index.**

## Key Rules

Project-specific technical rules — each stated in ≤2 lines; the named skill owns the full rule. Philosophy and process are Parts I–V; nothing here restates them.

- **The `charly` CLI is the ONLY operational interface — for you AND your agents.** Every build / deploy / probe / lifecycle operation on charly-managed resources goes through a `charly` verb — NEVER ad-hoc `podman` / `docker` / `virsh` / `systemctl` commands against them. A probe no `charly` verb expresses is a charly GAP to close as its own cutover, never a license for an ad-hoc command. See `/charly-internals:strict-policy` (R4 — the replacement table).
- **Lowercase-hyphenated names; cross-FILE cross-kind reuse is fine, but a single document's top-level node names are GLOBALLY UNIQUE.** The unified node-form is name-first, so every entity flattens to a top-level `<name>:` key: cross-FILE name reuse across SEPARATE discovered files (a layer `candy/redis` + an image `box/redis` — both `candy:` nodes, routed to distinct internal maps `uf.Candy` vs `uf.Box` by `base:`/`from:` presence) is still permitted, but two top-level entities WITHIN one document (the root `charly.yml`: a `pod` + a `vm` + a `local`, etc.) MUST NOT share a name — they would collide on one YAML key. `charly box validate` flags a collision; rename one (the convention: keep the user-facing deploy name, suffix the template it inherits — a `check-local` `local:` deploy + `check-local-app` local template; a `cachyos-gpu` `vm:` deploy + `cachyos-gpu-workstation-vm`). Verbs still disambiguate by command context, and `ResolveDeployRef` is image-first (`--add-candy <name>` for the layer-first path). See `/charly-image:layer`, `/charly-core:deploy`, `/charly-build:validate`.
- **`charly.yml` is the only filename** for image + layer definitions and the only file a project needs: per-dir discovery (`box/<name>/charly.yml`, `candy/<name>/charly.yml`), the remaining kinds inline in the project root. **There is ONE entity kind — `candy:` — not a `box:`/`candy:` pair:** a `candy:` node carrying `base:` (external base) or `from:` (a builder ref) is a full IMAGE (the former `box:`), one carrying neither is a LAYER fragment; the loader routes to `uf.Box` vs `uf.Candy` by that marker. The `charly box` COMMAND family and the `box/<name>/` discovery dir are UNCHANGED — only the YAML `box:` KIND keyword was removed. See `/charly-image:image`, `/charly-image:layer`, `/charly-build:migrate`.
- **Init-system polymorphism via mixed `service:` entries** — same `name:`, one `use_packaged:` form, one `exec:` form; the init system at deploy time renders only the match. NEVER a `<name>-host` / `<name>-pod` sibling candy. See `/charly-image:layer` "Service Declaration"; canonical example `/charly-infrastructure:virtualization`.
- **Tests ship with the image — MANDATORY per candy.** Every candy MUST carry a non-empty `description:` string AND a `plan:` with ≥1 deterministic `check:` step (the unified step vocabulary — one intent keyword `run:`/`check:`/`agent-run:`/`agent-check:`/`include:` carrying prose + an inline `Op`, each step a child node — the candy's ONE flat operational set); `charly box validate` hard-errors otherwise. See `/charly-check:check`. **Per-box `check_level:`** (`none`|`build`|`noagent` (default)|`agent`) gates how deep `charly check run <bed>` drives acceptance. See `/charly-image:image`.
- **Unified YAML + `import:` (Go-style namespaces)** — bare-string flat imports or single-key `alias: ref` namespaced children; a residual legacy `include:` is a hard load-time error pointing at `charly migrate`; `distro:`/`build:` inherit across a namespace boundary but `builder:` does NOT. See `/charly-image:image`, `/charly-internals:go`.
- **Every YAML file is a generic kind-container, routed by SHAPE — never by filename.** `discover:` is a flat generic scan-spec list; the schema version is a CalVer string, migrated by the single idempotent `charly migrate`; deployment nesting is tree position — a resource node placed under another deploys into it. See `/charly-internals:go`, `/charly-build:migrate`, `/charly-image:image`.
- **Per-kind versioning: `version:` is the authoritative identity** — mandatory CalVer on every candy; an image's emitted `ai.opencharly.version` label is the content-derived `EffectiveVersion`, stable across no-change builds. See `/charly-build:validate`, `/charly-internals:capabilities`.
- **Candy-version resolution is per-entity, post-fetch** — the `@github…:vTAG` tag is only the fetch coordinate; one arbiter warns only on real divergence (newest wins); `charly box reconcile` aligns the pins. See `/charly-internals:go` "Remote-layer resolver", `/charly-build:reconcile`.
- **Deploy fetches NOTHING speculative.** `charly bundle add` (any target kind) emits zero image-pull / image-build steps; test-bed image preflight belongs to the check entry point. See `/charly-local:local-spec`, `/charly-check:check` "Image preflight".
- **Mode purity** (a build-mode `LoadUnified` reads the PROJECT `charly.yml` only — never the per-host overlay) **and project directory resolution.** See `/charly-internals:go` "Mode purity", `/charly-image:image` "Project directory resolution".
- **User policy: adopt over rename** — declarative via `distro.<name>.base_user:` + `user_policy:`. See `/charly-image:image`, `/charly-build:build`.
- **Capabilities as OCI-label contract.** See `/charly-internals:capabilities`.
- **Deploy substrates** — a deploy's substrate kind at the edge picks where it lands: `local:` (direct shell, or SSH when the `host:` FIELD names a machine), `vm:`, `k8s:`, `android:`, `pod:` (the default) running a box `image:`, or `group:` (a targetless deploy group of resource members — no own workload) — all consuming the shared InstallPlan IR. A host/remote deploy MUST be authored as the `host:` FIELD on a `local:` deploy (`local: {from: <template>, host: <user@machine>}`) — there is NO `host:` venue KIND, and authoring a `host:` node is a hard load error. A Calamares package group MUST use the `package-group:` kind; `group:` is EXCLUSIVELY the targetless deploy group, never a package group. See `/charly-core:deploy`, `/charly-internals:install-plan`.
- **Cross-deployment probing via sibling members — ONE deployment tests ANOTHER** over the shared `charly` network (a resource node placed under a deploy is a sibling member, addressed by the unified `${HOST:<member>}` — bare for the member's container DNS, `${HOST:<member>:<port>}` for a host-reachable endpoint); sibling members inherit the owner's disposability and are never check-live'd. See `/charly-check:check` "Cross-deployment probing", `/charly-core:deploy` "Sibling members".
- **Android is a first-class deploy substrate** — a `kind: android` entity is a DEVICE; `apk:` is the candy-declared package format ONLY an `android:` deploy targeting a device executes; `pod → android` nesting mirrors `vm → k8s`. See `/charly-check:android`, `/charly-check:adb`, `/charly-check:appium`.
- **k3s clusters provision via candies** — k3s-server + k3s-agent compose on any substrate; kubeconfig returns via the candy `artifact:` block. See `/charly-infrastructure:k3s`, `/charly-kubernetes:check-k8s`.

## Where things are documented

The doc split is **five-way** — each layer has ONE owner; the others link to it, never restate it:

- **Rules & mandates → `CLAUDE.md`** (this file): R0–R10, the pillars as operational mandates, the cutover + post-execution process, the Key Rules index.
- **Features & command reference → `README.md`**: the user-facing intro and the build → run → deploy → evaluate command surface.
- **Usage & architecture → skills** (`plugins/README.md` is the full index): every candy, box, verb, and subsystem — the single source of truth for *how*.
- **Thesis & direction → `VISION.md`**: the long-term "why this exists and where it's going", stated as aspiration; enforced here via **Enforcing VISION.md**.
- **History → each repo's `CHANGELOG/`** (one file per CalVer release version, `<YYYY.DDD.HHMM>.md` — the version computed once and shared by the changelog filename and the release git tag): every dated change, past rename, completed cutover, retired identifier, and "previously / formerly / was". Every repository in the project — superproject, `plugins`, `pkg/*`, each `box/<distro>` — keeps its OWN `CHANGELOG/`; history is repo-scoped, never centralized in one file. Everything else describes the CURRENT state in present tense ONLY; the repo's `CHANGELOG/` is the sanctioned historical context named by R5's grep self-test.
