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

The loader implements the derivation correctly — `spec/spec/member_tree.go` defines
the two positions and `sdk/loaderkit/node_tree.go`'s `BuildResourceMemberChildren`
stamps them from the authored depth alone:

```go
pos := spec.PositionDeployLevel
if inBody[rk.Name] { pos = spec.PositionInSubstrate }
```

**The position IS authorable — and already authored — but the TOP-LEVEL kind gate cannot
see it.** A measured asymmetry, established on this thread:

- **The member path handles in-body members today.** `check-structkind-member`
  (`charly/charly.yml:1445`) and `inner-app-pod` (`:2084`) are in-body members, handled on
  the member path, with `check-structkind` live. The member-path emitters strip the member
  key before a body is validated: `EntityBodyJSON` (`sdk/loaderkit/node_tree.go:121`) does
  `delete(asMap, ch.Name)` at `:134-136`.
- **The top-level `#<Kind>Value` gate does not strip.** It validates the authored body
  verbatim — `charly/charly/provider_kind_invoke.go:424`:

```go
entity, err := requireProjectLoader().CueDocFromJSON("node "+pn.Name, pn.Body)
```

  `pn.Body` still carries the member keys (they ARE the position channel), so the closed
  `#DeployValue` arm (`spec/schema/node.cue:130`) rejects the member key. Measured on
  `check-group` after re-indenting a member into the `vm:` body:

```
node "check-group": vm: 5 errors in empty disjunction:
    "check-group-member": field not allowed:   (node check-group:1:2)
```

**So the defect is an ASYMMETRY, not an authoring gap:** the SAME body is stripped for the
member path and handed unstripped to the top-level gate. A bed author who tries the in-body
spelling is rejected by the gate and falls back to the **sibling** (deploy-level) spelling —
and a deploy-level `local:` member is walked as its **own root**
(`sdk/deploykit/deploy_tree.go` walks only `root.InSubstrateMembers()` into the parent venue),
so it deploys on the **host**, not the guest. That is the exact behaviour `30cabf27` (`#79`)
moved the beds into VMs to prevent.

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
| **Loader** (`sdk/loaderkit/parse.go:201`) | a member key inside a **core resource kind** body classifies as `PositionInSubstrate` — the parse parent-classification | the substrate check alone misses a core resource kind, so an in-body member of one is misread as deploy-level and rooted on the host |
| **Gate** (`charly/charly/provider_kind_invoke.go:424`) | the top-level `#<Kind>Value` gate validates the node's **stripped** body — `EntityBodyJSON(pn)`, the SAME emitter the member path already uses | ONE substitution; the seam already exists (`spec/spec/loader_seam.go:410-412`). **No per-kind CUE edit** — the seam is generic, so nesting behaves identically for every plugin |
| **Guard** (`sdk/loaderkit` validate) | the load-error control (end-state 5) | **its own chain, producer-first:** it fires at project resolve, so it lands together with the org-wide marking of every deliberate host-side member (`host: local`). Prevents the class, not the instances |
| **Walk** (`sdk/deploykit/deploy_tree.go`, `sdk/loaderkit/deploy_load.go`) | descend in-substrate, root deploy-level — the ONE walk | already correct; keep it the ONLY reader of position |
| **Migrate** (`candy/plugin-migrate` `reshape_group_deploy.go`) | a group unroll places unrolled members **in-body** (in-substrate) | so `charly migrate` never re-introduces the sibling-for-inside shape |
| **Beds** (`charly/charly.yml`, `distro-arch`, `distro-cachyos`) | each "inside" member moves into its parent body; each bed asserts the GUEST-side marker | the assertion FAILS if the effect lands on the host |

## Migration & landing order (producer-first)

1. **`sdk`** — the parse parent-classification (`sdk/loaderkit/parse.go:201`): a member key inside a **core resource kind** body classifies `PositionInSubstrate`. Unit test: such a member loads as in-substrate. **No `spec` leg** — the CUE bodies are not opened per kind; the seam is already generic.
2. **`charly`** — the gate substitution (`charly/charly/provider_kind_invoke.go:424`): hand `validateKindValueCUE` the stripped body via `EntityBodyJSON(pn)`. Bed `check-group` then loads and its member lands in the guest.
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
