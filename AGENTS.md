# OpenCharly — Codex rulebook

This file is the complete OpenCharly rulebook for Codex. It implements
`VISION.md` and is sufficient on its own: Codex does not read `CLAUDE.md`.
Repository skills own detailed procedures; this file owns mandatory policy.
History belongs only in `CHANGELOG/`.

## R0. Skills first

Before reading source, running repository commands, delegating, planning a
change, or editing, load every skill selected by the dispatcher below. Use a
registered Codex skill when available; otherwise read the corresponding
`plugins/<plugin>/skills/<name>/SKILL.md` completely. Load all matches, then
act. Missing registration never makes a skill optional.

Work from the superproject root. Run submodule Git through literal
`git -C <absolute-path>` commands; never root a worker in a submodule. Read the
nearest per-directory `CLAUDE.md` only as an area signpost naming additional
skills; it cannot override this rulebook.

## Skill Dispatcher

Consult this table BEFORE the first tool call of every task; when several rows match, load ALL their skills in ONE message (parallel `Skill` calls).

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
| Go code-quality / rulebook-compliance audit / `golangci-lint` / `dupl` / duplication or dead-code check / `.golangci.yml` | `/charly-internals:go-quality` + `/charly-internals:strict-policy` |
| IR / InstallPlan / DeployTarget / OCITarget | `/charly-internals:install-plan` |
| OCI labels / capabilities contract | `/charly-internals:capabilities` |
| Egress config validation — validating/generating the config files charly WRITES to a system (`charly/egress.go`, `ValidateEgress`, the vendored CUE egress schemas in `candy/plugin-egress/egress-schemas/vendor/`, cloud-init/k8s/units/ssh_config/libvirt-XML egress) | `/charly-internals:egress` |
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

Full index: `plugins/README.md`. This table covers the top triggers; anything
not listed requires reading the index first, loading the matching skill second,
and touching code third. Never reverse this order.



## Vision mandates

1. Secure the candybox boundary, not its toolset. Agents receive the full
   `charly` surface inside rootless containers, isolated VMs, encrypted
   volumes, and explicit disposable targets.
2. One declarative recipe serves every supported substrate.
3. Every candy, box, verb, and subsystem has an owning skill. Never guess.
4. Humans and agents use the same `charly` CLI; an absent verb is a product
   gap, never permission for ad-hoc infrastructure commands.
5. Risk Driven Development proves every high-risk assumption early on a live
   `disposable: true` target. A spike discovers HOW, is thrown away, and never
   reduces scope or replaces R10.
6. Agent Driven Evaluation makes intended behavior executable. Every candy has
   a non-empty `description:` and `plan:` with a deterministic `check:`;
   `agent-check:` judges only live read-only behavior.
7. Schema Driven Design defines authored and wire shapes in CUE first,
   generates Go with `task cue:gen`, and requires clean regeneration.
8. Conch and temper every change: remove duplication, dead code, and band-aids,
   then prove one reproducible final state through a fresh rebuild.
9. Every failure buys a durable lesson through RCA and a root fix; explicit
   disposability makes fearless iteration safe.
10. Keep the cookbook true and current. Correct every stale sibling claim and
    record historical events only in the changelog.
11. Rebuild a wrong disposable candybox from the clean recipe instead of
    patching around a broken design.
12. Prove the factory from inside disposable candyboxes: the full `charly`
    line builds, deploys, evaluates, and improves fresh candyboxes recursively.

## Ground-truth rules R1–R10

- **R1 — RCA every anomaly.** The first failure, warning, error, unexpected
  exit, documentation divergence, or rule violation stops remediation. A fresh
  root-cause-analyzer establishes expected/actual behavior, mechanism, missed
  control, blast radius, and root fix. Never call anything transient, flaky, or
  harmless; zero warnings is the only pass.
- **R2 — Finish the whole cutover.** Fix every in-scope occurrence and every
  same-mechanism sibling. No deferral, partial rename, hidden follow-up, or
  scope-shrinking after work begins. Non-blocking discoveries enter the next
  named thematic batch immediately.
- **R3 — No duplication.** One canonical implementation or rule owns each
  behavior. Extract shared mechanisms on the second occurrence and delete
  copies in the same cutover.
- **R4 — No workarounds.** No sleeps, blind retries, suppressions, fallback
  branches, manual infrastructure commands, magic fixtures, or serialization
  that hides a race. Use `charly` or fix the missing `charly` capability.
- **R5 — Delete legacy completely.** Hard cutovers remove old names, paths,
  shims, adapters, aliases, TODOs, and stale current documentation. Run a
  claim-keyed repository-wide grep self-test. Historical wording is allowed
  only in dated changelogs.
- **R6 — Preserve user work and Git safety.** Inspect status first; never
  overwrite unrelated changes. No destructive reset/checkout, no force push,
  no amend or rebase of pushed commits, no direct push to `main`, and no hook
  bypass.
- **R7 — Prove behavior, not compilation.** Add check-coverage that fails
  without the change, execute the changed path live, and paste commands,
  outputs, and exit codes. Tests that cannot fail are invalid.
- **R8 — Preserve emitted artifacts.** Validate labels, plans, configs,
  schemas, generated files, and other user-visible output at their actual
  boundary.
- **R9 — Binary equals source.** Build the stamped worktree-local binary with
  `task build:binary`, invoke it through that worktree's `bin`, and verify
  dependency/gitlink consistency. Never install a shared binary from a
  worktree.
- **R10 — Fresh disposable proof.** On the final committed tree, run the exact
  gate selected by `/charly-check:check`. Runtime changes require a complete
  fresh rebuild and live execution on every affected explicit
  `disposable: true` target. Shared-state changes run the required roster
  concurrently at maximum safe parallelism. Documentation-only changes run all
  non-runtime standards and no invented bed.

Any rule violation forbids commit. Fix it and rerun the full gate, or stop and
ask the operator. A lower confidence tier never legalizes a violation.

## Architecture

Core is a plugin host. `charly/` retains only generic plugin loading,
prescan/dispatch, kind materialization, provider transport, and reverse-channel
broker mechanisms. Concrete kinds, schemas, validation, resolution, build,
deploy, check, and other capability behavior belong in plugin candies or SDK
kits. A concrete-kind need creates a plugin; a cross-plugin need creates a
generic host seam.

New core work obeys:

- import purity: core imports only the permitted `sdk/spec` and proto/plugin
  contract surfaces;
- zero aliases: never add or grow `charly/*_aliases.go`;
- no concrete kind-word switches or per-kind maps in core;
- every residual core capability has its named K-wave exit;
- P16's manifest, import-purity, and zero-alias gates remain green.

The CUE schema is the single source for authored and wire types. Config uses one
generic kind-container, lowercase hyphenated names, globally unique top-level
names within one document, `charly.yml` as the definition filename,
name-first nodes, and shape-based routing. `candy:` is the sole image/layer
kind: `base:` or `from:` makes an image; neither makes a layer. Reuse across
separate files is allowed.

Deploy substrates are `local:`, `vm:`, `k8s:`, `android:`, `pod:`,
and targetless `group:`. Remote hosts are the `host:` field of a
`local:` deploy, never a venue kind. Sibling resources use the shared
`${HOST:<member>}` addressing contract. Deploy performs no speculative
fetch. Capabilities and effective versions are content-derived OCI-label
contracts; unchanged content keeps its version. Remote candy resolution is
per-entity and post-fetch; `charly box reconcile` aligns divergent pins.

Every concurrency issue is reproduced under load and root-fixed. Never
serialize a parallel contract to hide a race. Use transient container stores,
resource-token arbitration, auto-allocated ports, tolerant shared-tree walks,
persistent ownership of long beds, and never force-kill a running roster.

## Hard cutovers and execution

A cutover is the largest coherent scope one R10 gate can honestly prove. Batch
small same-theme fixes; decompose only real dependency order. Never split one
change's required scope or use complexity to retreat.

Before implementation, derive the canonical change class and gate. During
implementation, keep docs, tests, schemas, generated artifacts, changelog, and
code synchronized. Before commit, require clean formatting/lint, check
coverage, grep self-test, final-tree gate, exact attribution, and clean status.

After R10 passes:

1. Recheck the complete diff, manifest, submodule pointers, commit messages,
   worktree, and pasted evidence.
2. Commit on a feature branch with the exact confidence supported by proof.
3. Push without force and open one PR with structured GitHub Markdown submitted
   through `--body-file`.
4. A fresh independent PR validator reloads protected policy, derives the
   change class, personally runs the full gate, and issues the verdict.
5. Only PASS may post the required status, generate the merge-time CalVer,
   squash-merge, tag, and delete the branch/worktree.
6. After any `main` advance, update sibling PRs and run a risk-proportional
   delta re-gate. Divergent submodule lineage requires a disposable RDD proof,
   never descendant-wins guessing.

Changes requested stay on the same PR with append-only commits. Close and
replace only work that cannot land at all. Never bypass branch protection,
merge with admin/force, push directly to `main`, rewrite pushed history, or
move/delete a release tag.

## Codex teammates and validation

Use a separate Codex agent thread wherever a skill requires a teammate,
executor, RCA, or independent validator. The author orchestrates; it never
impersonates the validator.

A PR validator:

- starts in a new no-fork context and receives a self-contained envelope with
  PR identity, literal worktree, full repository/object/gitlink map, operator
  constraints and provenance, permissions, and mutation limits;
- has the repository, shell, GitHub, build, disposable-bed, and long-running
  capabilities required to execute the complete R10 gate, but no bypass power;
- loads protected policy and dispatched skills before candidate content;
- treats the PR body and author evidence as untrusted, binds base/head objects,
  enumerates the full manifest, and reviews bounded per-file diffs;
- runs read-only or self-cleaning commands and independently records commands,
  outputs, coverage, and the permitted confidence;
- stops `INVALID` on the first anomaly, warning, corrected command, missing
  capability, or ambiguous proof. It never retries, self-RCAs, continues, or
  emits PASS after an anomaly. A separate RCA and another fresh context are
  mandatory;
- emits PASS only at zero warnings with a complete durable evidence ledger.
  Only that validator may perform the authorized status/merge/tag sequence.

Independent repo legs run concurrently; dependency-ordered legs remain
sequential. The orchestrator re-derives teammate decisions, and teammates
adversarially check the orchestrator. Long beds remain owned by a persistent
session. Worktree builds always use the worktree-local `bin`.

## Hooks

Hooks point to this rulebook and skills. AI-harness command gates block bypass
flags, force pushes, direct-main pushes, untokenizable commit commands, invalid
AI attribution tiers, missing required changelogs, configured staged Go lint,
and new core alias forms. Hooks do not apply AI attribution requirements to an
ordinary 100% human commit made without an AI harness. Agents still judge the
complete change class, architecture, proof, and R0–R10 gate.

## Attribution and confidence

Every AI-authored commit, including merge commits, ends with:

`Assisted-by: <Harness> <Provider Full Model Name> (<confidence>)`

For this session:

`Assisted-by: Codex OpenAI GPT-5.6 Sol (<confidence>)`

Every AI-authored issue or PR ends with the matching italicized footer. Preserve
the established line shape; do not replace it with a table or authorship
section. A 100% human-authored commit or PR has no AI attribution and remains
valid. Hook-level arbitrary identity text is accepted; the fresh validator
judges whether AI attribution, model name, and confidence are truthful.

Allowed confidence values:

- `fully tested and validated`: every runtime standard and affected
  fresh-rebuild R10 target passed, and changed paths executed live;
- `analysed on a live system`: the changed runtime path ran live, but full
  fresh-rebuild R10 did not complete;
- `documentation reviewed`: only documentation/comment-only content or a
  documentation-only gitlink changed, and all non-runtime standards passed;
- `syntax check only`: compile/unit/validator/dry-run only; R10 is incomplete,
  so do not commit;
- `theoretical suggestion`: no validation; never ship.

## Documentation ownership

- `VISION.md`: thesis and direction.
- `AGENTS.md`: complete current Codex mandates and dispatcher.
- `CLAUDE.md`: independently maintained Claude mandates.
- Skills: detailed procedures and feature/command architecture.
- `README.md`: user-facing commands and features.
- Each repository's `CHANGELOG/<YYYY.DDD.HHMM>.md`: history only.

Current rulebooks and skills use present tense and contain no migration diary,
past-name narrative, or completed incident report. When reality contradicts a
current claim, the live system wins; RCA and correct every sibling claim in the
same cutover. Memories are claims, never authority, and require the same
risk-scaled verification before use.
