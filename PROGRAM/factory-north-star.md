# Agent-in-the-Loop Factory — Program North-Star

The binding north-star document for the Factory program, named in every spawn brief. The
charly repo's `CLAUDE.md` + `AGENTS.md` are the rulebook; this file restates no rule.
Detailed HOW lives in skills (`plugins/README.md` is the index). This document holds the
concrete end-state, vocabulary, the containment model, ordered decision heuristics, the
cutover sequence, observed anti-patterns, and measured state. On a task-vs-north-star
conflict a teammate stops and asks, never resolves locally.

Claims below marked **[proven upstream]** are verified against opencharly.ai; claims
marked **[HOW — spike it]** are this program's high-risk assumptions, to be proven early
on live `disposable: true` beds per RDD before any plan rests on them.

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
| **Forgemaster** | the only Factory Worker whose resource is configured with a `charly mcp serve` endpoint at all — every other Factory Worker gets none; forges and reaps spikes with ordinary charly verbs — the CLI is the only operational interface |
| **the six** | Sentinel (triage; R1/root-cause-analyzer is its job description), Forgemaster, Replicator (snapshot → hydrate → replay → bisect), Fixer (authors changes, only inside spikes), Verifier (drives beds, pastes proof — a delegate report is a claim, not proof), Archivist (ships verified fixes as Skills) |

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
  into the spike's *nested* store is a named HOW **[HOW — spike it]** (candidate
  mechanisms: build-inside via the nested charly, or `charly cp` an OCI archive +
  load; the winning verb, if missing, is a gap to close in charly, not a script).
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
  `charly vm cp-box <vm> <image> --rootless --domain <bed>`. Whether
  rootless k3s can run acceptably *inside* a nested-container spike is an optimization
  to prove later, never assumed **[HOW — spike it]**.
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

## The venue concept: one recipe, three flavors

A spike reproduces PROD **wherever PROD lives**. Four of the five substrates consume
the same InstallPlan IR **[proven upstream]** (local/vm/pod/android); the
`kubernetes:` substrate reads OCI labels + the cluster entity instead — so the flavor
changes keywords, never the recipe:

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
   did not shape. charly has **no helm word today** — the gap cutover 2 closes. Spike =
   disposable `vm:` composing `k3s-server` + `helm`, plan installs the chart at PROD's
   pinned version/values via `step:helm-release`, proven by `verb:helm` + `kube:`.

Forgemaster selects the flavor from the incident record. Replicator's hydration and the
regression bed are flavor-independent: they speak only the controller REST API and
MinIO, which every flavor exposes identically — the venue is below them.

## What ships where

- `spec/schema/*.cue` + per-plugin `.cue` — the helm step/verb shapes and the
  `agentteams:` verb shape. SDD without exception: schema first, `task cue:gen`,
  generated wire types only; an uncertain shape gets a schema spike.
- `candy/helm` — tool layer: helm binary, non-empty `description:`, `plan:` with
  deterministic `check:` steps (the ADE floor; `charly box validate` enforces it).
- `candy/plugin-helm` — out-of-process plugin via the SDK, registering
  `step:helm-release` (`repo, chart, version, release, namespace, values,
  values_files, wait, timeout` — field set to be confirmed by the schema spike) and
  `verb:helm` (release exists / status `deployed` / revision ≥ N / values hash).
  Venue-honouring realisation: `pod:`/`vm:`/`local:` execute `helm upgrade --install`
  against the venue's kubeconfig; the `kubernetes:` arm emits a `helmCharts:` kustomize
  entry and shells out to nothing. Zero helm knowledge enters the kernel — boundary law
  + import-purity apply; a kind-word switch in core is an incomplete seam, never a kept
  exception.
- `candy/plugin-agentteams` — extended with `verb:agentteams` (`status`,
  `workers_ready`, `room_exists`), reusing the existing `command:agentteams` REST
  client.
- `candy/agentteams-snapshot` — layer: export Worker/Team/Human via the controller API
  → YAML, PII-redact all room/Tuwunel references, `mc mirror` referenced objects, emit
  one hydration bundle. Composed into `agentteams-worker` so Replicator carries it.
- `candy/agentteams-factory` — layer shipping the seven Factory Skills (forge-spike,
  snapshot-incident, replay-incident, run-check-bed, diff-agent-variants,
  promote-change, teardown-spike) and the six Worker + Team + Human resource YAMLs as
  candy files — the files are the rollback units.
- root `charly.yml` — the `agentteams-factory` box (per the agentteams box precedent
  at 2664, joining the six boxes main already owns — `agentteams`,
  `agentteams-manager`, `agentteams-worker`, `check-k8s-deploy-app`,
  `docs-site-app`, `marketplace-app`): agentteams composition + factory + snapshot +
  `container-nesting`; the helm layer joins only the vm-flavor guests.
- `charly.yml` deploy entries — the PROD deploy (never disposable), spike templates
  `factory-spike-native` (`pod:` + nesting) / `factory-spike-kustomize` /
  `factory-spike-helm` (`vm:` + k3s), all `disposable: true`; beds `check-helm`,
  `check-factory-spike-<flavor>`.
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
   else is built on it; its findings name the delivery verb cutover 4 implements.
2. **The helm words.** Schema spike for `#HelmReleaseStep` on a live disposable
   `k3s-server` guest → `spec/schema` + per-plugin `.cue` → `candy/helm` +
   `candy/plugin-helm` → `check-helm` bed green at zero warnings → docs sync.
3. **`verb:agentteams`** into `candy/plugin-agentteams`, replacing the raw `http:` +
   `command:` curl + host-CLI mix in the agentteams beds in the same tree (R3 — a fix
   covers every surface).
4. **`candy/agentteams-snapshot`** with a round-trip fixture bed.
5. **The Factory proper**: `candy/agentteams-factory`, the box in root `charly.yml`
   (per the agentteams box precedent at 2664), PROD deploy + three spike templates +
   `check-factory-spike-*` beds proving the loop end
   to end (forge → hydrate → replay → repro-as-check → fix → bed green → teardown).
6. **Skill tree + dispatcher rows** in `plugins/`, wired into the existing adapters.

## Skill Dispatcher additions (for the repo CLAUDE.md table, cutover 6)

| Trigger | Skill to load |
| --- | --- |
| Factory / incident spike / improvement spike / forge-spike / spike flavor / regression-bed growth | `/charly-agentteams:factory` |
| `step:helm-release` / `verb:helm` / helm flavor / kustomize `helmCharts:` emission | `/charly-kubernetes:helm` |
| `verb:agentteams` / controller REST probing / hydration via `charly agentteams apply -f` | `/charly-agentteams:agentteams` |

Per-directory CLAUDE.md files under the new candies are thin signposts naming these
skills; they restate no rule. (`/charly-agentteams:agentteams` exists today as the
recipe card of the `charly-agentteams` plugin; the other two names are proposals until
cutover 6 lands them.)

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
- A spike forging a spike — spike Worker resources exclude the forge-spike
  Skill (`worker create --skills` scopes per Worker) and spike candyboxes get no charly
  MCP endpoint; depth = 1 by construction.
- Real credentials below PROD — a spike's `AGENTTEAMS_LLM_API_KEY` (deploy-overridable
  env **[proven upstream]**) is a scoped, budget-capped consumer key minted at PROD's
  Higress gateway, revoked at teardown; the minting mechanics are upstream-Higress
  capability, wired and proven in cutover 5 **[HOW — spike it]**.
- "Flake", "transient", blind retry, "pre-existing", "out of scope" — forbidden
  framings; Sentinel triages with root-cause-analyzer or not at all.
- Helm logic in the kernel, or a `kubernetes:` arm that shells out to helm instead of
  emitting a `helmCharts:` entry — boundary-law violations, tracked never kept.
- Hardcoding :8090 — resolve `${HOST_PORT:8090}` or read `charly agentteams config`.

## Measured state

- [ ] Cutover 1: containment spike green — nested-socket agentteams proven in one
      `pod:` candybox, delivery mechanism named, findings folded back into this file
- [ ] Cutover 2: `check-helm` green; provider index shows `step:helm-release` + `verb:helm`
- [ ] Cutover 3: agentteams beds assert via `verb:agentteams`; zero raw-`http:` remnants (grep self-test)
- [ ] Cutover 4: snapshot round-trip bed green
- [ ] Cutover 5: `check-factory-spike-native|kustomize|helm` green at zero warnings, pasted
- [ ] Cutover 6: dispatcher rows live; skills install through the existing adapters
- [ ] First real incident: reproduced in a spike, repro committed as a check, fix
      landed via pr-validator with the Human approval recorded

Update the boxes in the same change that earns them; a checked box without pasted R10
output is a claim, not proof.