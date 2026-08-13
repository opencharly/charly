# OpenCharly — Agent rulebook

This is the complete, harness-neutral OpenCharly rulebook for agents that read
`AGENTS.md`. It implements `VISION.md` and is sufficient on its own.
Repository skills own detailed procedures; this file owns mandatory policy.
History belongs only in `CHANGELOG/`.

## R0. Skills first

Before reading source, running repository commands, delegating, planning, or
editing, load every skill selected by the dispatcher below. Use a registered
project skill when available; otherwise read the corresponding
`plugins/<plugin>/skills/<name>/SKILL.md` completely. Load all matches before
acting. Missing registration is a project-profile defect, not permission to
skip the skill.

Work from the superproject root. Run submodule Git through literal
`git -C <absolute-path>` commands; never root a worker in a submodule. Use this
dispatcher and `plugins/README.md` to discover every applicable skill. A tool
action before R0 admission is a violation: stop, run the root-cause-analyzer
process, and re-derive conclusions after loading the required skills.

## Skill Dispatcher

Consult this table BEFORE the first tool call of every task. When several rows
match, load ALL their skills before doing anything.

<!-- BEGIN GENERATED SKILL DISPATCHER -->
| Trigger (what the user said or you're about to do) | Skill to load |
|---|---|
| Agent Driven Evaluation (ADE) / `charly box feature run` / `charly check feature run` / `charly feature list/pending/validate` / authoring a candy's `plan:` + `description:` string / the agent grader for `agent-check:` steps | `/charly-check:check` |
| Agent Driven Evaluation (ADE) / `charly box feature run` / `charly check feature run` / `charly feature list/pending/validate` / authoring a candy's `plan:` + `description:` string / the agent grader for `agent-check:` steps | `/charly-internals:strict-policy` |
| Authoring a plugin (a candy with a `plugin:` block) / builtin vs out-of-tree plugin / per-plugin `.cue` schema (single source → `gengotypes` for dev + schema-over-`Describe` RPC at runtime) / the plugin SDK (`github.com/opencharly/sdk`, the `sdk/` submodule) / `sdk/**` / a compiled-in plugin candy (`compiled_plugins:`) or host-coupled kit candy / an external plugin module | `/charly-internals:plugin` |
| CachyOS images / `cachyos*` / `charly-cachyos` workstation profile / `box/cachyos` submodule | `/charly-distros:cachyos` |
| CachyOS images / `cachyos*` / `charly-cachyos` workstation profile / `box/cachyos` submodule | `/charly-local:charly-cachyos` |
| CachyOS images / `cachyos*` / `charly-cachyos` workstation profile / `box/cachyos` submodule | `/charly-vm:cachyos-bootstrap-vm` |
| Debian images / `debian*` / `box/debian` submodule | `/charly-coder:debian-coder` |
| Debian images / `debian*` / `box/debian` submodule | `/charly-distros:debian` |
| Debian images / `debian*` / `box/debian` submodule | `/charly-distros:debian-builder` |
| Debian images / `debian*` / `box/debian` submodule | `/charly-distros:debian-debootstrap` |
| Debian images / `debian*` / `box/debian` submodule | `/charly-vm:debian-debootstrap-vm` |
| Disposable-flag semantics / `disposable: true` authorization / preemptible-flag / `requires_exclusive:` / `charly preempt` / exclusive host-resource arbitration (GPU passthrough contention) | `/charly-core:deploy` |
| Disposable-flag semantics / `disposable: true` authorization / preemptible-flag / `requires_exclusive:` / `charly preempt` / exclusive host-resource arbitration (GPU passthrough contention) | `/charly-internals:disposable` |
| Editing `local.yml` / authoring `kind: local` templates | `/charly-local:local-spec` |
| Editing `sdk/schema/*.cue` / `task cue:gen` / `cue exp gengotypes` / generated `cue_types_gen.go` / Schema Driven Design (SDD) / a schema spike | `/charly-internals:go` |
| Editing `sdk/schema/*.cue` / `task cue:gen` / `cue exp gengotypes` / generated `cue_types_gen.go` / Schema Driven Design (SDD) / a schema spike | `/charly-internals:plugin` |
| Editing a box (`box/<name>/charly.yml` — boxes live in the `box/<distro>` submodules; main owns none), box composition | `/charly-image:image` |
| Editing a candy (`candy/<name>/charly.yml`), candy authoring, candy tasks/services | `/charly-image:layer` |
| Egress config validation — validating/generating the config files charly WRITES to a system (`candy/plugin-fleet/egress.go`, `ValidateEgress`, the vendored CUE egress schemas in `candy/plugin-egress/egress-schemas/vendor/`, cloud-init/k8s_object/units/ssh_config/libvirt-XML egress) | `/charly-internals:egress` |
| Engineering-discipline triggers (failure surfaced / dup pattern / ad-hoc fix tempting / "out of scope" framing) | `/charly-internals:strict-policy` |
| Evaluate/audit a deployment config (image or deploy, yours) | `/charly-check:check` |
| Evaluate/audit a deployment config (image or deploy, yours) | `/charly-internals:agents` |
| Fedora images / `fedora*` / `box/fedora` submodule (incl. the GPU base `nvidia` / `python-ml` + `sway-browser-vnc`) | `/charly-coder:fedora-coder` |
| Fedora images / `fedora*` / `box/fedora` submodule (incl. the GPU base `nvidia` / `python-ml` + `sway-browser-vnc`) | `/charly-distros:charly-fedora` |
| Fedora images / `fedora*` / `box/fedora` submodule (incl. the GPU base `nvidia` / `python-ml` + `sway-browser-vnc`) | `/charly-distros:fedora` |
| Fedora images / `fedora*` / `box/fedora` submodule (incl. the GPU base `nvidia` / `python-ml` + `sway-browser-vnc`) | `/charly-distros:fedora-builder` |
| Fedora images / `fedora*` / `box/fedora` submodule (incl. the GPU base `nvidia` / `python-ml` + `sway-browser-vnc`) | `/charly-distros:fedora-nonfree` |
| Fedora images / `fedora*` / `box/fedora` submodule (incl. the GPU base `nvidia` / `python-ml` + `sway-browser-vnc`) | `/charly-distros:fedora-test` |
| Fedora images / `fedora*` / `box/fedora` submodule (incl. the GPU base `nvidia` / `python-ml` + `sway-browser-vnc`) | `/charly-distros:nvidia` |
| Git/`gh` workflow — `feat/` branch, commit, PR-only landing (NO direct push to main), branch protection, the `pr-validator` fresh-evaluator merge/tag, CalVer-at-merge, worktree, sync-to-upstream, branch/worktree prune, cross-repo R10 landing | `/charly-internals:git-workflow` |
| Go code-quality / CLAUDE.md-compliance audit / `golangci-lint` / `dupl` / duplication or dead-code check / `.golangci.yml` | `/charly-internals:go-quality` |
| Go code-quality / CLAUDE.md-compliance audit / `golangci-lint` / `dupl` / duplication or dead-code check / `.golangci.yml` | `/charly-internals:strict-policy` |
| Go source work (adding/modifying `charly` commands) | `/charly-internals:go` |
| Hard-cutover concerns / rename sweeps | `/charly-internals:cutover-policy` |
| IR / InstallPlan / DeployTarget / OCITarget | `/charly-internals:install-plan` |
| Managed `~/.config/charly/ssh_config` fragment / `charly vm create` writes Host stanza | `/charly-local:local-deploy` |
| Managed `~/.config/charly/ssh_config` fragment / `charly vm create` writes Host stanza | `/charly-vm:vm` |
| OCI labels / capabilities contract | `/charly-internals:capabilities` |
| Secret management / `charly secrets` / Secret Service / GPG `.secrets` | `/charly-build:secrets` |
| Skill authoring / skill maintenance / where does this doc content belong | `/charly-internals:skills` |
| Sub-agents / dynamic workflows / agent teams / agent-lifecycle or commit-push gate hooks | `/charly-internals:agents` |
| Ubuntu images / `ubuntu*` / `box/ubuntu` submodule | `/charly-coder:ubuntu-coder` |
| Ubuntu images / `ubuntu*` / `box/ubuntu` submodule | `/charly-distros:ubuntu` |
| Ubuntu images / `ubuntu*` / `box/ubuntu` submodule | `/charly-distros:ubuntu-builder` |
| Ubuntu images / `ubuntu*` / `box/ubuntu` submodule | `/charly-distros:ubuntu-debootstrap` |
| Ubuntu images / `ubuntu*` / `box/ubuntu` submodule | `/charly-vm:ubuntu-debootstrap-vm` |
| Unexpected failure / error / anomaly | `/charly-internals:root-cause-analyzer` |
| Verify a cutover by running the R10 beds (drive `charly check run <bed>`) | `/charly-check:check` |
| Verify a cutover by running the R10 beds (drive `charly check run <bed>`) | `/charly-internals:agents` |
| VmSpec / libvirt / cloud-init / OVMF internals | `/charly-internals:vm-spec` |
| `charly box build` / `charly box generate` / Containerfile | `/charly-build:build` |
| `charly box build` / `charly box generate` / Containerfile | `/charly-build:generate` |
| `charly box build` / `charly box generate` / Containerfile | `/charly-internals:generate-source` |
| `charly box reconcile` / cross-repo `@github` pin alignment / candy-version-mismatch cleanup | `/charly-build:reconcile` |
| `charly box validate` / schema error | `/charly-build:validate` |
| `charly check *` (ANY check verb, incl. `charly check box`) / `charly check run <bed>` (the disposable-deploy R10 bed) / authoring `disposable: true` check beds / `charly check live` / the probe verbs (cdp/wl/dbus/vnc/mcp/record/spice/libvirt) / `iterate:` AI-agent scoring / `plan:` step authoring / `charlycheck/*` branches | `/charly-check:check` |
| `charly clean` / build-artifact retention / `keep_images` / `keep_check_runs` / image-tag pruning / `.check` run cleanup | `/charly-core:clean` |
| `charly docs` / the opencharly.ai site / the `docs/` submodule / `task docs:sync`/`docs:drift` / Starlight/Astro / `candy/plugin-docs` (runtime plugin) or `candy/docs-site` | `/charly-build:docs` |
| `charly docs` / the opencharly.ai site / the `docs/` submodule / `task docs:sync`/`docs:drift` / Starlight/Astro / `candy/plugin-docs` (runtime plugin) or `candy/docs-site` | `/charly-tools:docs-site` |
| `charly fleet add/del` / pod or container deploys | `/charly-core:deploy` |
| `charly migrate` / schema migration / legacy → latest CalVer / CalVer schema version | `/charly-build:migrate` |
| `charly update` / `charly vm *` / VM entities in `vm.yml` or `vm:` | `/charly-internals:vm-deploy-target` |
| `charly update` / `charly vm *` / VM entities in `vm.yml` or `vm:` | `/charly-vm:vm` |
| `kind: android` device / `target: android` deploy / `apk:` package format in candies / installing Android apps declaratively / remote-or-emulator adb endpoint / nested `pod → android` | `/charly-check:android` |
| `kind: android` device / `target: android` deploy / `apk:` package format in candies / installing Android apps declaratively / remote-or-emulator adb endpoint / nested `pod → android` | `/charly-core:deploy` |
| local-target deploy / `target: local` / `host: local` (default) / SSH-host deploys / `user:` / `ssh_arg:` | `/charly-internals:local-infra` |
| local-target deploy / `target: local` / `host: local` (default) / SSH-host deploys / `user:` / `ssh_arg:` | `/charly-local:local-deploy` |
| the `adb:` check verb / Android Debug Bridge probing from a candy/box plan (out-of-process plugin; devices, shell, install, getprop, screencap, logcat, wait-for-device) | `/charly-check:adb` |
| the `adb:` check verb / Android Debug Bridge probing from a candy/box plan (out-of-process plugin; devices, shell, install, getprop, screencap, logcat, wait-for-device) | `/charly-check:check` |
| the `appium:` check verb / Android UI automation (out-of-process plugin) / W3C WebDriver sessions, element introspection, the gesture/app/key/device sugar groups, the generic `execute`/`raw` escape hatch | `/charly-check:appium` |
| the `appium:` check verb / Android UI automation (out-of-process plugin) / W3C WebDriver sessions, element introspection, the gesture/app/key/device sugar groups, the generic `execute`/`raw` escape hatch | `/charly-check:check` |
| the `kube:` check verb / Kubernetes cluster probing from a candy/box plan (out-of-process plugin; nodes, pods, ingress, wait-ready, storageclass, addons, apply/delete, raw resource GETs) | `/charly-kubernetes:check-k8s` |
<!-- END GENERATED SKILL DISPATCHER -->

Full index: `plugins/README.md`. Anything not listed requires reading the index
first, loading the matching skill second, and touching code third.

## Candyboxing

Secure the candybox boundary, not its toolset. People and agents use the same
full `charly` CLI inside rootless containers, isolated VMs, encrypted volumes,
and explicitly disposable targets. One declarative recipe serves every
supported substrate. An absent verb is a product gap, never permission for an
ad-hoc infrastructure command.

Every candy, box, verb, and subsystem has an owning skill. Every candy has a
non-empty `description:` and executable `plan:`. Rebuild a wrong disposable
candybox from the clean recipe instead of patching around it, and prove the
factory from inside fresh disposable candyboxes.

## Risk Driven Development (RDD)

Prove every high-risk assumption early on a live target explicitly marked
`disposable: true`. Risk is the trigger: if being wrong invalidates the plan,
is costly to reverse, or derails RCA, documentation and source inspection are
not proof. Correct contradicted documentation in the same change.

A spike answers one named high-risk unknown. It is time-boxed and thrown away
after its proven mechanism is recorded. It never reduces scope, ships as the
implementation, or replaces R10. A discovery that changes the contract requires
operator direction.

## Memory hygiene

Treat every saved system fact as a claim. R1 establishes that it is real; RDD
proves high-risk claims before they are saved. Keep preferences narrow and
dated where relevant, verify named artifacts before reuse, and correct or
delete stale memory when live evidence disagrees.

## Agent Driven Evaluation (ADE)

Each candy's `plan:` is its acceptance test and contains at least one
deterministic `check:`. Each plan item has exactly one intent: `run:` changes
state, `check:` probes idempotently, `agent-run:` may mutate, `agent-check:`
assesses live state read-only, and `include:` composes another plan. Parse
errors, timeouts, and failed grading fail the step.

## Schema Driven Design (SDD)

Define authored configuration and host/plugin wire shapes in CUE before code.
Generate Go with `task cue:gen`; never hand-transcribe schema-shaped wire
structs. Validation, migration, plugin inputs, and egress derive from the same
schema. Clean regeneration is a no-op. Any generation exception requires RCA
and a live schema spike under the owning skill.

## Prioritize Clean Architecture Above All Else

Conch every change: remove duplication, dead code, aliases, band-aids, and
misplaced behavior before proving one reproducible final state. Complexity,
compatibility convenience, and sunk effort never justify weakening the target
architecture.

## The kernel/plugin boundary law

Core is a generic plugin host. It owns only plugin loading, prescan/dispatch
(including the per-node kind-decode registry resolve + provider invoke a
plugin's Materializer seam calls back into — the fold/not-found POLICY itself
is a plugin), provider transport, and the reverse-channel broker. Concrete
kinds, schemas, validation, resolution, build, deploy, and check behavior
belong in plugin candies or SDK kits. A concrete-kind need creates a plugin; a
cross-plugin need creates a generic host seam.

Core imports only permitted contract surfaces, gains no kind-word switches or
per-kind maps, and never adds or grows alias files. The CUE schema remains the
single source for authored and wire types. Detailed placement, transport,
concurrency, generated-artifact, and egress contracts live in their owning
skills and are mandatory when dispatched.

## Ground-truth rules R1–R10

- **R1 — RCA every anomaly.** The first failure, warning, error, unexpected
  exit, documentation divergence, or rule violation stops remediation. A fresh
  root-cause-analyzer establishes expected and actual behavior, mechanism,
  missed control, blast radius, and root fix. Zero warnings is the only pass.
- **R2 — Finish the whole cutover.** Fix every in-scope occurrence and
  same-mechanism sibling. No deferral, partial rename, hidden follow-up, or
  scope shrinking after work begins.
- **R3 — No duplication.** One canonical implementation or rule owns each
  behavior. Extract shared mechanisms on the second occurrence and delete
  copies in the same cutover.
- **R4 — No workarounds.** No sleeps, blind retries, suppressions, fallback
  branches, manual infrastructure commands, magic fixtures, or serialization
  that hides a race. Use `charly` or fix the missing capability.
- **R4a — Fix the product first; documentation never routes around a defect.**
  When a documented behaviour and the code disagree, establish which holds the
  INTENT, then fix the code BEFORE touching the prose. Editing docs to match a
  bug is forbidden; so is editing them to avoid one. A `git clone`, a `cd`, a
  `-C` standing in for a flag that should work, an "except on…" caveat, or a
  "known limitation" note in place of a fix all describe the defect instead of
  removing it — and make it permanent by making it look intended. Every command a
  reader is told to run MUST work with nothing but the `charly` binary
  installed — no clone, no `cd`, no pre-existing project, no unnamed companion
  tool. That is the product's purpose, not a documentation nicety: install one
  binary, and everything the docs show works. Write every page for a reader who
  installed `charly` as a native package with NO charly checkout anywhere on the
  machine: no `task` target, no `./bin/charly`, no repo-relative path, no verb
  supplied by a candy that exists only in this repository. `--repo
  <owner>/<repo>` reads a published project without a clone; `charly box new
  project <dir>` starts a local one. The one exception is the INSTALL page,
  where the reader obtains charly — it leads with the native package and scopes
  the checkout to working ON charly. A repository-maintenance command is named
  as maintenance this project performs, never as a step the reader runs. Anywhere else, needing more than the binary is a
  PRODUCT DEFECT — the missing capability is the bug, and the fix belongs in
  `charly`. If the fix is genuinely
  out of scope, file it with its root cause and let the page say nothing rather
  than something false: silence is recoverable, a documented workaround is
  taught.
- **R5 — Delete legacy completely.** Hard cutovers remove old names, paths,
  shims, adapters, aliases, TODOs, and stale current documentation. Run a
  claim-keyed repository-wide grep self-test. History belongs in changelogs.
- **R6 — Preserve user work and Git safety.** Inspect status first; never
  overwrite unrelated changes. No destructive reset/checkout, force-push,
  pushed-history rewrite, hook bypass, or direct push to `main`.
- **R7 — Prove behavior, not compilation.** Add coverage that fails without
  the change, execute the changed path live, and retain commands, outputs, and
  exit codes. Tests that cannot fail are invalid.
- **R8 — Preserve emitted artifacts.** Validate labels, plans, configs,
  schemas, generated files, and other user-visible output at their actual
  boundary.
- **R9 — Binary equals source.** Build the CalVer-stamped worktree-local binary
  with `task build:binary`, invoke it through that worktree's `bin`, verify its
  version and dependency/gitlink consistency, and never install it as a shared
  binary. Runtime OS dependencies belong in `pkg/arch/PKGBUILD`.
- **R10 — Fresh disposable proof.** Run the exact gate selected by
  `/charly-check:check` on the final committed tree. Runtime changes require a
  complete fresh rebuild and live execution on every affected explicit
  `disposable: true` target. Shared-state rosters run concurrently at maximum
  safe parallelism. Documentation-only changes run every non-runtime standard
  and no invented bed.

Any rule violation forbids commit. Fix it and rerun the full gate, or stop and
ask the operator. A lower confidence tier never legalizes a violation.

The core Go gate runs `go test ./...` and `go vet ./...` from `charly/`, then
`task build:binary` from the superproject and confirms `bin/charly version`.
Never run bare module-wide Go commands from the superproject. Preserve
individual terminal proof when an aggregate task omits declared commands.

## Disposable-Only Autonomy

Autonomous mutation is authorized only on targets explicitly marked
`disposable: true`. Never infer disposability from a name, environment, or
operator habit. Use the owning deploy/check command for creation, cleanup, and
reconciliation; do not bypass it with substrate tools.

High-risk and runtime work may iterate freely on those targets, but final proof
still uses a fresh rebuild from the final committed tree. Never interrupt,
retry, force-kill, or clean an active long-running bed merely because its
terminal stream is quiet; require its process exit and current `summary.yml`.

## Hard Cutover by Default

A cutover is the largest coherent scope one R10 gate can honestly prove. Batch
small same-theme fixes and decompose only real dependency order. Keep code,
tests, schemas, generated artifacts, documentation, and changelogs synchronized.
Remove every old identifier and sibling claim in one phase; do not leave shims,
dual paths, TODOs, or deferred cleanup.

Use a linked feature worktree from current protected `origin/main` for
substantial work and preserve the operator's checkout. **Every session works in
its OWN worktree under `.claude/worktrees/<slug>/`, branched off fresh
`origin/main`; the main tree stays on `main`.** Create the worktree at session
start and remove it (worktree + branch) after the change lands, so parallel
sessions never collide on one checkout. Verify status, HEAD,
merge-base, submodule lineage, and remote base before implementation and again
before landing. Existing normal caches may be used only through the runtime's
approved boundary; never manufacture alternate homes, caches, clones, or
validator workspaces to make a command pass. A denied required action is
`BLOCKED`. *Detail:* `/charly-internals:git-workflow` (B1 step 0, B4 "Worktree
hygiene", B7).

## Post-Execution Policies

After the final gate:

1. Recheck the complete diff, manifest, gitlinks, changelogs, attribution,
   worktree state, and retained evidence.
2. Commit on a `feat/` branch at the confidence supported by proof, push without
   force, and open one PR with structured Markdown supplied by `--body-file`.
3. A fresh independent `pr-validator` reloads protected policy, binds the exact
   base and head, personally runs the derived gate, and posts its durable
   verdict before gated actions.
4. Only PASS may post `charly/pr-validator`, generate the merge-time CalVer,
   squash-merge with the bound head, tag the merge, and clean the branch and
   worktree. A changed head, warning, anomaly, or live unfinished bed revokes
   PASS.
5. After `main` advances, update interacting PRs and run a risk-proportional
   delta gate. Never guess across divergent submodule lineage.

Changes requested during review use append-only commits on the same PR. Never
bypass branch protection, use admin/force, rewrite pushed history, move a
release tag, or merge your own unvalidated work.

## Acceptance checklist

Before declaring completion, answer every applicable item YES:

- RDD proved every high-risk assumption early.
- Every anomaly and stale claim received RCA before remediation.
- The cutover has no surviving legacy path, duplication, workaround, or stale
  current documentation.
- Coverage fails without the change and validates real emitted artifacts.
- The real changed source produced the artifact under test.
- The exact final-tree R10 change-class gate passed with zero warnings.
- The approved plan completed with no hidden phase, TODO, or substitute.
- Every repository landed through one attributed squash commit, a fresh
  independent validator, protected merge, and immutable merge-time tag.

## Agents, Workflows & Teams

Use addressable agents for bounded independent work and fresh judgment; the
author remains responsible for briefs, integration, architecture, and evidence.
Use a fresh root-cause-analyzer for R1 and a fresh independent `pr-validator`
for landing. Never impersonate either role or pass author output off as
independent evidence.

Long-running beds remain owned by a persistent session. Delegated executors
return verbatim commands, outputs, outcomes, and exit codes; the orchestrator
retains and reports them. Fresh context means independent judgment, not a new
clone, cache, worktree, or weaker permission model. Detailed agent roles,
handoffs, validator phases, and workflow mechanics belong to
`/charly-internals:agents` and `/charly-internals:git-workflow`.

## Hooks

Hooks enforce only deterministic command mechanics such as bypass flags,
force-push, direct-main push, untokenizable commit commands, configured staged
lint, and forbidden alias forms. There is no reminder-hook layer: rule
knowledge lives in the rulebooks and skills. The fresh validator alone judges attribution truth, change class,
changelog coverage, architecture, and R0–R10 proof. Hooks guard mechanics;
agents judge policy and evidence.

## AI Attribution (Fedora Policy Compliant)

Every AI-authored commit, including a merge commit, ends with:

`Assisted-by: <Harness> <Provider Full Model Name> (<confidence>)`

Use the exact harness, provider, and full model name exposed by the authoring
runtime. Every AI-authored issue or PR ends with the matching italicized line.
A 100% human-authored contribution carries no AI attribution.

| Confidence | Required proof |
|---|---|
| `fully tested and validated` | Every runtime standard and affected fresh-rebuild R10 target passed; changed paths executed live. |
| `analysed on a live system` | The changed runtime path ran live with retained output, but the complete fresh-rebuild R10 gate did not pass. |
| `documentation reviewed` | Only documentation, comment-only edits, or a documentation-only gitlink changed, and every non-runtime standard passed. |
| `syntax check only` | Compile, unit, validator, or dry-run proof only; R10 is incomplete, so do not commit. |
| `theoretical suggestion` | No validation; never ship. |

`documentation reviewed` is forbidden if code or behavioral configuration also
changed. Runtime confidence is forbidden for prose-only work. Confidence never
excuses a policy failure: any rule violation forbids commit.

## Key Rules

- The `charly` CLI is the only operational interface for managed resources.
- One canonical CUE schema owns authored and wire shapes; generated Go must be
  reproducible.
- One `charly.yml` generic kind-container uses lowercase hyphenated names and
  shape-based routing; top-level names are unique within a document.
- `candy:` is the sole image/layer kind; `base:` or `from:` makes an image.
- Every candy ships a description and deterministic executable check plan.
- Capabilities and effective versions are content-derived OCI-label contracts.
- Concurrency is proven under load and races are root-fixed, never hidden by
  retries or serialization.
- Runtime plugins stamp only usable committed source provenance; failures
  remain errors at their source.
- Strict operator commands and idempotent internal reconciliation are separate
  contracts.

The named skills own the full technical rules. Do not expand this index into a
second copy of them.

## Where things are documented

- `VISION.md`: thesis and direction.
- `AGENTS.md`: complete current harness-neutral mandates and dispatcher.
- `CLAUDE.md`: complete harness adapter with equivalent overall policy.
- `plugins/<plugin>/skills/<name>/SKILL.md`: detailed procedures and technical
  ownership.
- `plugins/README.md`: complete skill index.
- `README.md` and current subsystem docs: present behavior and user guidance.
- [opencharly.ai](https://opencharly.ai) (the `docs/` submodule): the public site — a small
  hand-authored narrative plus a reference/recipe catalog GENERATED from the sources above by
  `charly docs generate`. Never hand-edit a generated page; fix the source and run
  `task docs:sync`.
- `CHANGELOG/`: historical events, retired names, and migration narrative.
