# Agent Teams Factory — Program North-Star

The binding north-star document for the Agent Teams Factory program, named in every
spawn brief. "The Agent-in-the-Loop Factory" is the *product's* name (see `README.md`);
this program builds the multi-agent subsystem that runs on it, and the two are
deliberately distinct scopes. The charly repo's `CLAUDE.md` + `AGENTS.md` are the
rulebook; this file restates no rule. Detailed HOW lives in skills
(`plugins/README.md` is the index). This document holds the concrete end-state,
vocabulary, the containment model, the trust boundaries, ordered decision heuristics,
the cutover sequence, observed anti-patterns, and measured state. On a
task-vs-north-star conflict a teammate stops and asks, never resolves locally.

Claims below carry one of three evidence tags. **[proven upstream]** — verified against
opencharly.ai. **[proven by the containment spike]** — established by unit 1's throwaway
RDD spike, whose bed no longer exists in the tree; the finding survived, the bed did not.
**[HOW — spike it]** — this program's high-risk assumptions, to be proven early on live
`disposable: true` beds per RDD before any plan rests on them.

## End-state (concrete)

charly's `agentteams` family gains a Factory subsystem: six Workers on a production
AgentTeams deployment that forge **incident spikes** and **improvement spikes** —
disposable candyboxes carrying a faithful copy of PROD — reproduce failures there, prove
fixes against ADE plans, and land changes through the normal PR gate with a Human
approval recorded in the Incident Room. Every reproduced incident becomes a permanent
`check:` step on the regression bed — proven on the disposable spike bed, then committed
to the regression bed's `plan:` at incident close. The Factory is charly's own RDD loop
applied to operating a multi-agent system: the candy factory builds factories.

Everything ships in-tree as candies, plugins, boxes and beds — no companion repository.
Factory *deployment data* (incident snapshots, minted keys) lives in deploy volumes,
never in git.

## Vocabulary (Factory words, in charly's words)

| Factory word | What it is in charly terms |
| --- | --- |
| **incident spike** | a time-boxed, throwaway, `disposable: true` deploy carrying the agentteams composition, forged to prove one named HOW: "this PROD failure reproduces here." Like every spike it never ships and never replaces R10 |
| **improvement spike** | the same disposable deploy, forged to score a candidate change (Skill version, runtime, model routing) against the regression bed before promotion |
| **spike flavor** | which PROD deployment style the spike reproduces — `native`, `kustomize`, or `helm` (below) |
| **spike bed** | the spike's check bed. `disposable: true` carries the full `charly check run` cycle — build → check → deploy → steady → check live → destroy → rebuild → check → teardown — for free **[proven upstream]** |
| **regression bed** | the accumulated `plan:` of every closed incident: ADE's "the spec is the test," grown one incident at a time — `incident close --as-check` appends the incident's committed `check:` (proven on the disposable spike bed) to the regression bed's `plan:`, so the bed fails without the fix and passes with it (R10) |
| **Forgemaster** | the only Factory Worker holding the host's disposable-LIFECYCLE surface (Verifier also runs on the host, but only to drive bed runs — it forges and destroys nothing): forge (fleet add of pre-authored `disposable: true` templates), `check run`, `update`, `del`, status/logs. Today `charly mcp serve` exposes every verb, so this scoping is a stated gap: the `--disposable-only` tool filter is a gap to close in charly (unit 7), not a prompt-level promise **[HOW — spike it]** |
| **the six** | Sentinel (triage; R1/root-cause-analyzer is its job description; PROD read-only), Forgemaster (forge-host, disposable-lifecycle surface only), Replicator and Fixer (**run INSIDE the spike** — candyboxing applied to the Factory's own agents: the spike's nested charly is their whole, unrestricted candy store, and the boundary is the security model), Verifier (the existing `check-bed-runner`/`deploy-verifier` executor pattern; pastes verbatim proof), Archivist (ships verified fixes as Skills; git surface only) |

## The containment model: candyboxing all the way down

This is the section the whole program hangs on, so it is exact.

**The candybox is the security boundary — and charly candyboxes nest.** The dev boxes
already carry rootless podman/buildah/skopeo inside a container with no `--privileged`
(the `container-nesting` candy; "run charly inside charly"), and rootless libvirt is
the separate `candy/virtualization` candy — so a candybox can build and deploy
candyboxes **[proven upstream]**. The uid-1000 posture comes from how the *outer*
container is launched, not the candy. The AgentTeams controller spawns Manager/Worker
containers through a container-runtime socket **[proven upstream]** — *which* socket
it is handed decides the containment story:

- **A spike hands the controller its own nested rootless podman socket.** The entire
  spike — controller, homeserver, gateway, MinIO, *and every Manager/Worker the
  controller spawns* — then lives inside one disposable candybox with its own image
  store. One boundary, one teardown, nothing escapes into the host store. This is the
  default spike: a single `pod:` deploy composing the agentteams composition +
  `container-nesting`. Delivering the pre-built `agentteams-manager`/`-worker` images
  into the spike's *nested* store is **[proven by the containment spike]**: the
  container-venue analog of `charly vm cp-box`'s streamed transfer: host
  `podman save <image>` piped into the spike's nested uid-1000 store via
  `charly shell <spike> -c 'podman --remote --url unix:///run/user/1000/podman/podman.sock load'`,
  no intermediate tarball. The images are private on ghcr.io (anonymous manifest
  fetch is 401), so the registry hop is not an option — local delivery is the
  mechanism, and the winning verb (`charly box load`, unit 5) is a gap to close
  in charly, not a script. Unit 5 landed it, and unified both venues onto one path —
  `sdk/deploykit.TransferImageToVenue` over `spec/container.StreamLoad` — so the
  VM-only `streamLoadAndTag` this paragraph once named no longer exists. The controller's reconcile loop self-heals: once the
  image lands in the nested store, the controller spawns the manager without a
  restart, so delivery can happen after the controller is up.
- **The nested socket path needs a runtime-dir volume.** `/run` is a fresh
  root-owned tmpfs at container start, so the build-time `/run/user/1000` pre-creation
  (the controller candy's plan step) does not survive — the uid-1000 service cannot
  create it (root-owned `/run`). The spike template must declare a named volume at
  `/run/user/1000`, initialized from the image's uid-1000-owned `/run/user/1000`
  (the WHOLE dir chowned to 1000, not just the `podman/` subdir — the nested podman
  writes its `libpod` runtime dir there too). **[proven by the containment spike]**
  Unit 5 re-proved both halves on its own bed and closed the two questions unit 1
  left open. First, the build-time seed DOES survive into the image, so the copy-up
  has a source — that was the open risk in the named-volume approach, and a
  build-context check now asserts it. Second, a tmpfs is not an alternative: podman's
  `--tmpfs` rejects `uid=`/`gid=` (`unknown mount option "uid=1000"`), so a tmpfs at
  this path can only ever be root-owned. The named volume is the ONE mechanism that
  yields a uid-1000-owned runtime dir on a pod venue.
- **Serving the socket is its own candy, and unit 1 never recorded how.** The
  containment spike proved a controller running on the nested socket but its bed was
  throwaway, and the mechanism died with it — `podman system service` appeared nowhere
  in the tree afterwards, so the finding was unreproducible. `candy/nested-podman-socket`
  (unit 5) is that mechanism made permanent: a supervisord `exec:` entry running
  `podman system service` as uid 1000, plus the runtime-dir volume above. It is
  deliberately SEPARATE from `container-nesting` — nesting supplies podman, this
  supplies its API endpoint, and the nine boxes composing nesting have no need of a
  podman service. **[proven by the `check-boxload-pod` bed]**
- **The upstream `check-agentteams-pod` bed instead binds the HOST rootless podman
  socket into the box** **[proven upstream]** — deliberately, so spawned containers use
  the same store the images were built into (no registry hop). That is a *bed
  convenience for testing the stack itself* and is **forbidden for spikes**: with the
  host socket, spawned Workers land as siblings in the host store, outside the spike's
  boundary and beside PROD's own containers. A spike that leaks its workers has no
  boundary to license its autonomy.
- **`vm:` is the escalation and the cluster venue, not the default.** A disposable
  `vm:` guest (kernel-enforced isolation) is chosen when (a) the incident plausibly
  involves the kernel, drivers, or a container-escape hypothesis, or (b) the flavor
  needs a real Kubernetes cluster — our engineering judgment being that `k3s-server`
  runs cleanest under a guest's own systemd and cgroups. The guest pattern itself is
  **[proven upstream]**: `check-agentteams-vm` does `add_candy` into a cloud-image
  guest, systemd units at uid 1000, spawn images delivered with
  `charly vm cp-box <vm> <image> --rootless --domain <bed>` (the `--domain` flag is
  the BED name, not the entity). Whether rootless k3s can run acceptably *inside* a
  nested-container spike is an optimization to prove later, never assumed
  **[HOW — spike it]**.
- **Nesting is position in the file, and spikes use it only internally.** Deploys
  indented under the spike run inside its venue (e.g. a `local:` applying the overlay
  inside the k3s guest). A spike is **never** nested under the PROD deploy — the test
  must not live inside the system under test — and **never** a `group:` sibling/member
  of PROD, because members share a lifecycle and a spike's teardown must never be
  coupled to PROD's.

**What `charly fleet` is here — and is not.** The fleet is simply the set of deploys
charly manages on the forge host **[proven upstream]**; it is not a grouping or
placement mechanism and nothing is ever "placed into PROD's fleet". Concretely:
`charly fleet add` registers a spike as its own top-level disposable deploy on the host
where Forgemaster's charly runs (which may or may not be PROD's host); `charly
start|stop|status|logs|shell|cp` operate it; `charly fleet del` removes it; unattended
`charly update`/destroy on it is authorized by the `disposable: true` flag alone —
never inferred from its name. PROD is a long-lived deploy in some host's fleet
(`pod:` realised as user-level quadlets on podman+systemd hosts, or `vm:`) and is never
disposable.

## Trust boundaries: who holds which surface

Candyboxing, applied to the Factory's own agents — restrict by *boundary*, never by
tool list:

- **Inside a spike** (Replicator, Fixer): full nested charly, no filtering — the
  charly-native posture. Hydration bundles are delivered *into* the spike at forge
  time; the inside agent applies them with the spike's own `charly agentteams apply`.
  The fix loop IS an `iterate:` bed: the agent + charly run in the bed's `sandbox:`,
  scored by the bed's `check:` steps — shipped machinery, not new. Incident data is
  attacker-influenced input; inside-execution is the mitigation (injection wrecks at
  most one rebuild), and everything crossing back out — the repro check, the fix
  branch — is untrusted until the deterministic bed and the human PR gate pass it.
- **Forge host** (Forgemaster): disposable lifecycle only (above). The forge MCP
  endpoint MUST be reachable only from the forge host — stdio transport, or an
  explicitly loopback-bound listener. State this as the REQUIREMENT it is, not a
  property that holds today: `charly mcp serve --listen` defaults to `:18765`,
  which binds every interface, so a forge deployment that accepts the default is
  routable from PROD Workers and from spikes. Binding it down is a deployment
  obligation until unit 7 makes the forge surface enforceable rather than
  promised **[HOW — spike it]**.
- **PROD**: no agent-held write path at all. Promotion is a *git landing*: Fixer
  pushes a `feat/` branch from inside the spike (scoped deploy key, branch-namespace
  write only), pr-validator + Human approval land it, and the PROD apply is the
  operator's charly — not any agent's.
- **Credentials**: Forgemaster never holds gateway admin. Spike LLM keys come from a
  pre-minted, budget-capped key pool provisioned at PROD config time; the agent draws
  the next key and revocation is the pool's job **[HOW — spike it]**. Spike egress is
  restricted to the PROD Higress endpoint, so a compromised inside-agent cannot
  exfiltrate snapshot data elsewhere **[HOW — spike it]**.
- **No recursion, restated structurally**: inside-agents MAY nest deploys inside their
  spike — recursion *within* the boundary is charly's whole point. The invariant is
  that no spike reaches the forge host's charly: the boundary enforces what the old
  skill-exclusion rule merely requested.

## The venue concept: one recipe, three flavors

A spike reproduces PROD **wherever PROD lives**. Four of the five substrates consume
the same InstallPlan IR **[proven upstream]** (local/vm/pod/android — `deploy:android`
is served by `candy/plugin-adb`); the `kubernetes:` substrate reads OCI labels + the
cluster entity instead — so the flavor changes keywords, never the recipe:

1. **`native` flavor** — PROD is the charly-composed agentteams deploy. Spike = one
   disposable `pod:` candybox: agentteams composition + `container-nesting`, controller
   wired to the nested socket (the default containment above). Escalate to `vm:` on the
   kernel-suspicion trigger. Hydration: `charly agentteams apply -f` against the
   spike's controller at `${HOST_PORT:8090}`.
2. **`kustomize` flavor** — PROD is a `kubernetes:` deploy: the candy list emitted as a
   Kustomize overlay onto a real cluster. The substrate applies via
   `kubectl apply -k <overlay>` **[proven upstream]** — only `from-box --cluster` is
   write-only. Spike = disposable `vm:` composing `k3s-server`, with the *same emitted
   overlay* applied inside the guest by a nested step: the spike proves the overlay
   itself, not an approximation of it.
3. **`helm` flavor** — PROD runs the upstream AgentTeams Helm chart on a cluster charly
   did not shape. The helm words **landed in unit 2** (`candy/plugin-helm`, commit
   53cf8499; both arms proven live per CHANGELOG 2026.225.2248). Spike = disposable
   `vm:` composing `k3s-server` + the chart's install-leg candy, installing at PROD's
   pinned version/values via `step:helm-release`, proven by `verb:helm` + `kube:` —
   the `check-helm-vm` pattern.

Forgemaster selects the flavor from the incident record. Replicator's hydration and the
regression bed are flavor-independent: they speak only the controller REST API and
MinIO, which every flavor exposes identically — the venue is below them.

## What ships where

- `spec/schema/*.cue` + per-plugin `.cue` — the helm step/verb shapes and the
  `agentteams:` verb shape. SDD without exception: schema first, `task cue:gen`,
  generated wire types only; an uncertain shape gets a schema spike.
- `candy/helm-chart` — **[landed]** the `check-helm-vm` bed's install leg, carrying a
  law the unit surfaced: a bed's own plan runs verify-only and `charly fleet add`
  lowers only CANDY plans' `run:` steps — so any mutating install (the
  `step:helm-release` invocation, the in-venue node-ready `kubectl wait`) must live in
  a candy's `run:` steps, never in the bed's plan. Own ADE `check:` asserts the
  release exists (R3).
- `candy/plugin-helm` — **[landed, both arms proven live]** a standalone Go module
  served out-of-process over go-plugin gRPC via the SDK, registering
  `step:helm-release` — a plugin-contributed `external:helm-release` install-step KIND
  (F3) whose OPAQUE payload is `#HelmReleaseStep` (fields confirmed by the unit 2
  schema spike: `repo?`, `chart!`, `version?`, `release!`, `namespace?`, `values?`, …),
  carried through the InstallPlan IR, validated against the plugin's served schema at
  authoring; the step performs `helm upgrade --install` IN-VENUE against the venue's
  kubeconfig (a three-arm chain: an operator-set `KUBECONFIG`, else the k3s guest
  default `/etc/rancher/k3s/k3s.yaml`, else `$HOME/.kube/config` — arm 3 is what
  carries a non-k3s cluster) and returns a `helm uninstall` ReverseOp the host records
  and replays — and `verb:helm`, the declarative release-status assertion (`verb:kube`
  analog), EXEC-based over the live DeployExecutor reverse channel, owning NO Kubernetes
  client library. The `kubernetes:` substrate arm is the `helm_charts:` deploy field —
  which the k8sgen emitter translates into a kustomize `helmCharts:` transformer entry —
  plus the `--enable-helm` apply path, proven on `check-k8s-deploy`. Zero helm knowledge
  in the kernel — boundary law + import-purity hold.
- `candy/plugin-agentteams` — **[landed]** now serves `providers:
  [command:agentteams, verb:agentteams]`. The verb is HOST-BASED (the mcp pattern):
  resolves the controller's in-venue :8090 to a host-routable address over the reverse
  channel, pulls the admin SA token over the executor, probes with the SAME `apiClient`
  the command uses (R3 — one REST surface). Methods as built: `status`,
  `manager-running`, `worker-running` (idempotent Worker-CR create on 409, polls
  Running WITH its Matrix room provisioned, resolves the alias on :6167),
  `worker-list`. Dual placement (compiled-in `spec.CheckVerbProvider` in-proc / pb
  Invoke envelope out-of-process — placement-invisible, F8); skips under
  `charly check box`.
- `charly box load` — **[landed, unit 5]** the container-venue image-delivery verb the
  containment spike named: the analog of `charly vm cp-box` for a `pod:` venue's
  nested store. Closes the gap that forced a hand-run `podman save | podman
  --remote load` pipeline (R4). Both verbs are now bindings of ONE venue-generic path
  — `sdk/deploykit.TransferImageToVenue` (verified idempotency, the torn-overlay probe
  and its drop-and-re-stream recovery, the post-load tag) over
  `spec/container.StreamLoad` (the tarball-free pipe) — so a third venue costs a
  constructor, not a fourth copy. Both resolve a bare box name through the STRICT
  `ResolveBuiltImageRef`, which refuses an image older than the newest local build:
  delivering a stale artifact is the wrong-artifact class `charly check box` refuses
  to certify, and harder to notice, since the load succeeds and the venue simply runs
  the wrong image. (`vm cp-box` never actually had working short-name support — a
  local stub returning `""` shadowed the canonical resolver; unit 5 deleted it.)
- `candy/nested-podman-socket` — **[landed, unit 5]** the prerequisite the verb
  delivers INTO: the nested podman's API socket at uid 1000, plus the `/run/user/1000`
  named volume that makes it possible.
- `charly mcp serve --disposable-only` — **[unit 7]** the tool filter that makes the
  Forgemaster's license enforceable rather than promised.
- `candy/agentteams-snapshot` — layer: export Worker/Team/Human via the controller API
  → YAML, PII-redact all room/Tuwunel references, `mc mirror` referenced objects, emit
  one hydration bundle. Composed into `agentteams-worker` so Replicator carries it.
- `candy/agentteams-factory` — layer shipping the seven Factory Skills (forge-spike,
  snapshot-incident, replay-incident, run-check-bed, diff-agent-variants,
  promote-change, teardown-spike) and the six Worker + Team + Human resource YAMLs as
  candy files — the files are the rollback units.
- root `charly.yml` — the `agentteams-factory` box (per the agentteams box precedent,
  joining the six boxes main already owns — `agentteams`, `agentteams-manager`,
  `agentteams-worker`, `check-k8s-deploy-app`, `docs-site-app`, `marketplace-app`):
  agentteams composition + factory + snapshot + `container-nesting`; the helm layer
  joins only the vm-flavor guests.
- `charly.yml` deploy entries — the PROD deploy (never disposable), spike templates
  `factory-spike-native` (`pod:` + nesting) / `factory-spike-kustomize` /
  `factory-spike-helm` (`vm:` + k3s), all `disposable: true`; beds `check-helm-vm`
  (+ its `check-helm-vm-ctx` kubernetes profile), `check-factory-spike-<flavor>`.
- `plugins/` — the Factory owning skill + a bed-executor agent entry (verbatim-proof
  pattern), `model: inherit` like the rest of the roster.
- Docs — recipe cards in the candy sources; `charly docs generate` + `task docs:sync`;
  never hand-edit a generated page; every documented command works with nothing but the
  `charly` binary installed (R4a).

## Ordered cutover units

Each unit is one atomic hard cutover per repo, R10-gated on disposable targets, landed
PR-only through a fresh `pr-validator`, CalVer at merge, one `CHANGELOG/` file. RDD
front-loads the named HOWs.

1. **Containment spike (RDD, throwaway).** Prove on a live bed: agentteams composition
   + `container-nesting` in one `pod:` candybox, controller on the *nested* socket,
   spawn images delivered into the nested store, controller-spawns-manager →
   create-worker → room-exists green. This de-risks the default spike before anything
   else is built on it; its findings name the delivery verb unit 5 implements.
2. **The helm words — LANDED** (commit 53cf8499, CHANGELOG 2026.225.2248). Schema
   spike → per-plugin `.cue` → `candy/plugin-helm` + `candy/helm-chart` →
   `check-helm-vm` executed live against fresh disposable rebuilds, plus the
   `helm_charts:` emission + `--enable-helm` apply on `check-k8s-deploy`.
3. **`verb:agentteams` — LANDED** (commit 7f3175a2, CHANGELOG 2026.226.0946). The
   raw curl + host-CLI mix in `check-agentteams-pod` / `check-agentteams-vm` is
   replaced by `agentteams:` verb steps; the verb executed live against a fresh
   rebuild of the disposable `check-agentteams-pod` bed. The compiled-in dual-dispatch
   legs (`runPluginVerb` et al.) were the R10-gate catch, RCA'd live on the bed.
4. **Factory docs catch-up.** This document's revision, plus the Skill Dispatcher rows
   and the `/charly-kubernetes:helm` recipe card that units 2 and 3 shipped capability
   without. The dispatcher is a CURATED fast path, not an index — 60 of 307 skill
   entities carry `triggers:`, and anything absent falls back to the documented
   `plugins/README.md` route — so a missing row does not make a capability
   unreachable, it makes it easy to miss at the first tool call. For surfaces as
   central as the helm words and the whole agentteams family that is a real routing
   gap. The row and its skill land together, never split: a row naming a skill that
   does not exist is a false claim (R1). Documentation-only change class:
   non-runtime standards, no bed.
5. **`charly box load` — LANDED.** The nested-store image-delivery verb the containment
   spike named, gated by `check-boxload-pod`. It also had to ship the socket mechanism
   itself (`candy/nested-podman-socket`), because unit 1 recorded the FINDING that a
   nested socket works without recording HOW it was served — the throwaway bed took the
   mechanism with it. The unit unblocks the native spike in unit 8.
6. **`candy/agentteams-snapshot`** with a round-trip fixture bed.
7. **Make the forge surface enforceable.** Two gaps, one unit, because they are the
   same license: (a) `charly mcp serve --disposable-only` — spike first, since the
   filter may key on the target's `disposable:` flag, a verb allowlist, or both, and
   the answer decides the shape; (b) the listener default — `--listen` defaults to
   `:18765`, every interface, so an unhardened forge endpoint is reachable from PROD
   Workers and from spikes. Both must precede unit 8, which deploys a Forgemaster.
8. **The Factory proper**: `candy/agentteams-factory`, the box in root `charly.yml`,
   PROD deploy + three spike templates + `check-factory-spike-*` beds proving the loop
   end to end (forge → hydrate → replay → repro-as-check → fix → bed green →
   teardown). Too large for one honest gate, so it decomposes into ordered sub-units as
   forward motion: factory candy + box → native template + bed → kustomize + helm
   templates + beds → the key pool and egress restriction.
9. **`/charly-agentteams:factory`** — the Factory owning skill and its dispatcher row,
   authorable only once unit 8 exists.

## Skill Dispatcher additions (for the repo CLAUDE.md + AGENTS.md tables)

| Trigger | Skill to load | Lands in |
| --- | --- | --- |
| `step:helm-release` / `verb:helm` / helm flavor / kustomize `helmCharts:` emission | `/charly-kubernetes:helm` | unit 4 |
| `verb:agentteams` / controller REST probing / hydration via `charly agentteams apply -f` | `/charly-agentteams:agentteams` | unit 4 |
| Factory / incident spike / improvement spike / forge-spike / spike flavor / regression-bed growth | `/charly-agentteams:factory` | unit 9 |

Skills are GENERATED from a candy's `skill:` entity by `charly marketplace generate` —
never hand-written into `plugins/`. Per-directory CLAUDE.md files under the new candies
are thin signposts naming these skills; they restate no rule.
(`/charly-agentteams:agentteams` exists today as the recipe card of the
`charly-agentteams` plugin and only lacked a dispatcher row;
`/charly-kubernetes:helm` is written in unit 4; `/charly-agentteams:factory` remains a
proposal until unit 9 lands it.)

## Ordered decision heuristics

1. When a spike bed and any document — including this one — disagree, the bed wins; fix
   the document in the same change (a documentation divergence is an R1 incident).
2. Containment before convenience: the nested socket is the default; the host socket is
   a bed-only pattern and never a spike pattern; `vm:` is chosen by the two triggers
   (kernel suspicion, cluster venue), not by habit.
3. Flavor selection matches PROD's deployment style exactly; an approximate venue is a
   workaround (R4) — if the venue cannot be matched, the missing capability is the bug.
4. Anything the Factory cannot do through a `charly` verb is a gap to close in charly,
   never a license for hand-run `podman`/`kubectl`/`helm` against managed resources.
5. Repro before remediation: no fix lands without its incident committed as a
   deterministic `check:` on the regression bed (proven on the disposable spike bed),
   and coverage must fail without the change (R10).
6. Promotion is a landing: the Human approval event in the Incident Room is the
   operator's authorization; promote-change produces a `feat/` branch and a PR, never a
   direct write to a running PROD outside the resource YAMLs.

## Observed anti-patterns (forbidden framings included)

- Binding the host podman socket into a spike — the bed convenience that dissolves the
  spike's boundary; spawned Workers leak into the host store beside PROD's.
- Nesting a spike under the PROD deploy, or making it a `group:` member of PROD — the
  test inside the system under test; a coupled lifecycle.
- Hand-fixing a running spike — rebuild beats patch: change the recipe and let the
  bed's destroy/rebuild prove it reproducible, not merely successful.
- Marking PROD disposable "to simplify testing" — disposability is the one and only
  autonomy authorization and PROD never carries it.
- A spike reaching the forge host's charly — the boundary rule that replaced
  skill-exclusion: nesting *inside* a spike is fine, reaching *out* never is.
- Handing any agent the full `charly mcp serve` surface — an unfiltered forge endpoint
  can `stop`/`remove`/`shell` PROD and touch `local:`; the disposable-lifecycle filter
  is the license made enforceable.
- Accepting `charly mcp serve`'s default listen address for a forge endpoint —
  `--listen` defaults to `:18765`, every interface, so the default is the exposure.
  Bind loopback or use stdio until unit 7 lands.
- Gateway-admin credentials in any Worker — keys come from the pre-minted pool.
- Real credentials below PROD — a spike's `AGENTTEAMS_LLM_API_KEY` (deploy-overridable
  env **[proven upstream]**) is a scoped, budget-capped consumer key minted at PROD's
  Higress gateway, revoked at teardown; the minting mechanics are upstream-Higress
  capability, wired and proven in unit 8 **[HOW — spike it]**.
- "Flake", "transient", blind retry, "pre-existing", "out of scope" — forbidden
  framings; Sentinel triages with root-cause-analyzer or not at all.
- Helm logic in the kernel, or a `kubernetes:` arm that shells out to helm instead of
  emitting a `helm_charts:` entry — boundary-law violations, tracked never kept.
- Hardcoding :8090 — resolve `${HOST_PORT:8090}` or read `charly agentteams config`.

## Measured state

- [x] Unit 1: containment spike green — nested-socket agentteams proven in one
      `pod:` candybox, delivery mechanism named, findings folded back into this file
      (live proof: `charly check live check-factory-spike-full` — 91 steps: 78
      passed, 0 failed, 13 skipped as out-of-context; manager + worker spawned
      through the NESTED socket into the nested store, worker room provisioned and
      alias-resolvable. That bed was unit 1's own throwaway spike bed and is not in
      the tree — it is NOT one of the `check-factory-spike-<flavor>` beds unit 8
      creates, which remain unbuilt)
- [x] Unit 2: `check-helm-vm` executed live against fresh `disposable: true`
      rebuilds — `step:helm-release` install + `verb:helm` assertions, AND the
      `helm_charts:` kustomize emission + `--enable-helm` apply on `check-k8s-deploy`
      (CHANGELOG 2026.225.2248, class `fully tested and validated`); providers:
      `step:helm-release` + `verb:helm`
- [x] Unit 3: `check-agentteams-pod` / `-vm` assert via `agentteams:` verb steps
      (raw curl + host-CLI sequences replaced); the verb executed live against a fresh
      rebuild of the disposable `check-agentteams-pod` bed (CHANGELOG 2026.226.0946,
      class `fully tested and validated`)
- [x] Unit 4: dispatcher rows resolve; `/charly-kubernetes:helm` published; this
      document audited claim-by-claim against the tree (documentation-only class —
      the gate is the non-runtime standards, not a bed). The rows are live in
      `CLAUDE.md` and `AGENTS.md` and `plugins/kubernetes/skills/helm/` exists; the
      box went unchecked at the time and was corrected during unit 5
- [x] Unit 5: `charly box load` delivers an image into a nested uid-1000 store, bed
      green — `check-boxload-pod` PASS (13 steps), with `boxload-deliver` and
      `boxload-present-in-nested-store` passing on BOTH the initial and the
      fresh-rebuild live legs (36 steps: 31 passed, 0 failed). Coverage proven to
      discriminate by perturbation: with the `load` word removed the verb is rejected
      outright, so the delivery check fails. Shipped with `candy/nested-podman-socket`
      (the socket mechanism unit 1 never recorded) and the R3 unification of the four
      `save | load` paths onto `spec/container.StreamLoad` +
      `sdk/deploykit.TransferImageToVenue`
- [ ] Unit 6: snapshot round-trip bed green
- [ ] Unit 7: `--disposable-only` filter proven to refuse a non-disposable target,
      and the forge listener proven unreachable from a spike
- [ ] Unit 8: `check-factory-spike-native|kustomize|helm` green at zero warnings, pasted
- [ ] Unit 9: `/charly-agentteams:factory` installs through the existing adapters
- [ ] First real incident: reproduced in a spike, repro committed as a check, fix
      landed via pr-validator with the Human approval recorded

Update the boxes in the same change that earns them; a checked box without pasted proof
at the gate its change class requires is a claim, not proof.
