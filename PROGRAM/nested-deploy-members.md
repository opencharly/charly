# Nested Deploy Members — Program North-Star

The binding north-star for the **nested deploy member tree** program (the fix for
`charly#748`). This file is the contract: on a task-vs-north-star conflict a
teammate stops and asks, never resolves locally. The `charly` `AGENTS.md` is the
rulebook; this file restates no rule.

## Problem (measured, `charly#748`)

The deploy model intends **deploy-into nesting** — a resource node placed *under*
another resource node deploys INTO that parent's venue. The model, the docs, and
every bed's prose say so:

> *"Deploy-into nesting is **tree position** (a resource node placed under another
> resource), not a `nested:` field."* — `/charly-core:deploy`

The derivation itself is correct — `spec/spec/member_tree.go` defines the two
positions and `sdk/loaderkit/node_tree.go`'s `BuildResourceMemberChildren` stamps
them from the authored depth alone:

```go
pos := spec.PositionDeployLevel
if inBody[rk.Name] { pos = spec.PositionInSubstrate }
```

**Two defects sit on either side of that derivation** — the parse never hands it the
member, and then the top-level gate refuses the body it stamps. Both are measured on this
thread, in that order, and the order is why neither alone suffices.

### Defect 1 — the parse drops an in-body member of a core resource kind (`sdk`)

`sdk/loaderkit/parse.go:209` is the test for whether a nested key is a member *child*:

```go
if t.DeploySubstrates[disc] || resourceKindSet[disc] {   // was: if t.DeploySubstrates[disc]
```

`Threaded.DeploySubstrates` is populated in only two ways — a *connected* out-of-process
deploy provider, and a project's **declared** plugin words — while the core substrate
words reach the loader from the compiled-in `plugin-substrate` as `kind` providers, whose
deploy backends run out-of-process. So at parse time `vm` is absent from that set, the
test misses, and the member is left as opaque data: the node never gets the child, so the
derivation never runs for it. Measured — the regression test fails with the change
reverted:

```
parse_substrate_parent_test.go:47: node "check-group": want 1 member child, got 0
  (body={"check-group-member":{"local":{"from":"check-group-app"}},"description":"group bed"})
FAIL	github.com/opencharly/sdk/loaderkit
```

### Defect 2 — the top-level kind gate was handed the authored body (`charly`)

The member-path emitters strip the member key before a body is validated —
`EntityBodyJSON` (`sdk/loaderkit/node_tree.go:121`) does `delete(asMap, ch.Name)` at
`:134-136`. The top-level `#<Kind>Value` gate validated the authored body verbatim
(`charly/charly/provider_kind_invoke.go`, commit `41d59819`):

```go
// before — the authored body, member key and all
entity, err := requireProjectLoader().CueDocFromJSON("node "+pn.Name, pn.Body)
// after — the same stripped body the member path already emits
bodyJSON, err := requireProjectLoader().EntityBodyJSON(pn)
entity, err := requireProjectLoader().CueDocFromJSON("node "+pn.Name, bodyJSON)
```

`pn.Body` still carries the member key — it IS the position channel the derivation reads —
so the closed `#DeployValue` arm (`spec/schema/node.cue:130`) rejects the node. Measured on
`check-group` with its member re-indented into the `vm:` body:

```
node "check-group": vm: 5 errors in empty disjunction:
    "check-group-member": field not allowed:   (node check-group:1:2)
```

**Without the parse fix there is no member child at all**, so the emitted body is
identical to the authored one and the gate substitution is a byte-for-byte no-op.
**Without the gate strip there is a member child**, but the gate still sees its key and
refuses the node.

### What the corpus measured — the position authors actually got

No shipped bed carries an in-body member of a core resource kind. Both beds cited on this
thread as in-body members sit at **deploy-member indent** — a sibling of their kind key,
whatever their own comments say. Measured by indentation on `charly/charly.yml` at this
head:

```
1436| 4| check-structkind-vm:
1437| 8| vm:
1439| 8| # NESTED under the vm node (tree position) → the kind:lo…
1442| 8| check-structkind-member:      ← a SIBLING of `vm:` (indent 8), not a child
2081| 8| inner-app-pod:               ← the same shape
```

`check-group` at `origin/main` had that same shape — `vm:` and `check-group-member` both at
indent 4. So an author who wants the *into-the-venue* position is left with a spelling the
gate refuses, and the spelling that ships is the **alongside** one. An alongside `local:`
member is walked as its **own root** — `sdk/deploykit/deploy_tree.go:43` walks only the
root's `InSubstrateMembers()` into the parent venue — so it lands on the **host**, not in
the guest: the exact behaviour `30cabf27` (`#79`) moved the beds into VMs to prevent. This
head re-nests `check-group`'s member **into** the `vm:` body (commit `ed499734`), which is
the position these comments always claimed.

## End-state (concrete, single codepath)

**ONE authoring model, ONE derivation, ONE walk — used everywhere, no parallel paths.**

1. **Nesting is authored by tree position, and tree position is authorable.**
   A resource entity key placed **inside** a substrate kind body is a NESTED
   member (deploys INTO the parent's venue). A resource entity key placed as a
   **sibling** of the kind key is an ALONGSIDE member (its own root, shared
   network). Nothing else expresses placement — no `nested:`, no `host:`
   venue-kind, no per-substrate switch.

2. **The position is DERIVED from authored depth — never stored, never re-derived
   from the kind.** `spec.Member.Position` (already the model) is the ONE
   classification; `Member.InSubstrate()` / `Member.Alongside()` are its only
   readers.

3. **ONE walk descends into the venue.** `deploykit.WalkDeploymentTree` (deploy)
   and its teardown mirror walk **in-substrate** members into the parent venue and
   treat **deploy-level** members as their own roots. Every consumer
   (`candy/plugin-fleet`, `candy/plugin-deploy-vm`, `candy/plugin-deploy-local`,
   `sdk/deploykit`, `spec/deploy`) calls that ONE walk; none reads `Position`
   directly to branch.

4. **The venue hop is a plugin-declared trait, not a kind switch.** The descent
   (how to reach the parent's venue) is stamped by the substrate plugin
   (`spec/deploy` `DescentDescriptor` / `Transport`, via `kit.StampDescent`); the
   walk descends generically. This preserves the kernel/plugin boundary law: core
   is the generic host, the substrate owns "how to get inside me".

5. **No silent degradation (the missed control).** A **deploy-level `local:`
   member with no `host:`** beside a `vm:`/`pod:` parent is a **LOAD ERROR** with a
   remediation hint (`author it inside the parent body to deploy INTO the guest, or
   set host: local to deploy beside it on the host`). The host-deploying shape can
   never arise silently again. It **lands as its own chain** (landing order step 5),
   because it fires at project resolve and needs the org-wide `host: local` marking
   first.

## The single codepath — every layer

| Layer | The ONE thing | Notes |
|---|---|---|
| **Loader** (`sdk/loaderkit/parse.go:209`) | a member key inside a **core resource kind** body classifies as `PositionInSubstrate` — the parse parent-classification | the substrate check alone misses a core resource kind, so an in-body member of one is misread as deploy-level and rooted on the host |
| **Gate** (`charly/charly/provider_kind_invoke.go` — `validateKindValueCUE`, commit `41d59819`) | the top-level `#<Kind>Value` gate validates the node's **stripped** body — `EntityBodyJSON(pn)`, the SAME emitter the member path already uses | ONE substitution; the seam already exists (`spec/spec/loader_seam.go:410-412`). **No per-kind CUE edit** — the seam is generic, so nesting behaves identically for every plugin |
| **Guard** (`sdk/loaderkit` validate) | the load-error control (end-state 5) | **its own chain, producer-first:** it fires at project resolve, so it lands together with the org-wide marking of every deliberate host-side member (`host: local`). Prevents the class, not the instances |
| **Walk** (`sdk/deploykit/deploy_tree.go`, `sdk/loaderkit/deploy_load.go`) | descend in-substrate, root deploy-level — the ONE walk | already correct; keep it the ONLY reader of position |
| **Migrate** (`candy/plugin-migrate` `reshape_group_deploy.go`) | a group unroll places unrolled members **in-body** (in-substrate) | so `charly migrate` never re-introduces the sibling-for-inside shape |
| **Beds** (`charly/charly.yml`, `distro-arch`, `distro-cachyos`) | each "inside" member moves into its parent body; each bed asserts the GUEST-side marker | the assertion FAILS if the effect lands on the host |

## Migration & landing order (producer-first)

1. **`sdk`** — the parse parent-classification (`sdk/loaderkit/parse.go:209`): a member key inside a **core resource kind** body classifies `PositionInSubstrate`. Unit test: such a member loads as in-substrate. **No `spec` leg** — the CUE bodies are not opened per kind; the seam is already generic.
2. **`charly`** — the gate substitution (`charly/charly/provider_kind_invoke.go`, `validateKindValueCUE`): hand `validateKindValueCUE` the stripped body via `EntityBodyJSON(pn)`. Bed `check-group` then loads and its member lands in the guest.
3. **`plugin-migrate`** — unroll into the body.
4. **`charly` + `distro-arch` + `distro-cachyos`** — move the members in-body; re-pin.
5. **The `sdk` guard + the marking sweep — its own chain.** The load-error control (end-state 5) fires at project resolve, so it lands only once every deliberate host-side member in the org carries `host: local`.
6. **docs** — the gated pages (`start/quickstart.md`, `concepts/02`, `concepts/12`, `guides/containers-and-nesting.md`) follow the shipped behaviour, not before.

## Acceptance / R10

- **Unit (fails without the change):** the parse test — an authored member inside a **core
  resource kind** body classifies `PositionInSubstrate`. The guard's own test arrives with the
  guard's chain, not here.
- **Live (the class gate):** every affected bed runs its OWN disposable `check run`
  and asserts the **guest-side** marker — a regression to the sibling shape makes
  the marker land on the host and the bed FAIL. One `kind:check-roster` roster over
  the affected beds is the R10 gate.
- **Zero warnings** on the roster; `charly box validate` 0/0 — the guard's own leg adds its
  zero-warning run when it lands with the marking sweep.

## What this is NOT

- Not a per-bed workaround (each bed restructured ad hoc) — that leaves the
  asymmetry and the class alive.
- Not a **per-kind CUE edit** — opening each substrate body in turn would re-apply the same
  defect once per plugin instead of once at the one shared seam. The operator's direction is
  binding here: nesting must work EXACTLY the same everywhere, whatever the plugin.
- Not a `nested:` field, a `host:` venue-kind, or any second placement spelling —
  those are the parallel paths the member-tree cutover already deleted.
- Not a core kind-switch — the descent stays a plugin-declared trait.
