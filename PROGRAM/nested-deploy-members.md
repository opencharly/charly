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

**But the in-substrate position is NOT authorable.** The CUE schema is closed:
`#DeployValue` (`spec/schema/node.cue:130`) is `#Deploy & {nested?: _|_, peer?: _|_,
inside?: _|_, …}` and **no substrate kind body accepts an entity child** — a `vm:` /
`pod:` body has no member pattern. Measured: re-indenting a member into the `vm:`
body is **rejected** by `charly box validate`:

```
node "check-group": vm: 5 errors in empty disjunction:
    "check-group-member": field not allowed:   (node check-group:1:2)
```

And `#Local` (`spec/schema/local.cue`) no longer carries a `host:` field, so the
"explicit host" escape hatch is absent too.

**Consequence.** Every bed that intends *"inside the guest"* is forced into the
**sibling** (deploy-level) spelling — and a deploy-level `local:` member is walked
as its **own root** (`sdk/deploykit/deploy_tree.go` walks only
`root.InSubstrateMembers()` into the parent venue), so it deploys on the **host**,
not the guest. That is the exact behaviour `30cabf27` (`#79`) moved the beds into
VMs to prevent. Nesting is not unused — it is **impossible to author**: every bed
in the org passes `charly box validate` in CI, and the rejection pasted above is
that gate refusing the in-body spelling, so no shipped bed can carry one.

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
   never arise silently again.

## The single codepath — every layer

| Layer | The ONE thing | Notes |
|---|---|---|
| **Schema** (`spec/schema/*.cue`) | every substrate kind body (`vm:`/`pod:`/`local:`/`kubernetes:`/`android:`) accepts an entity-child map (a `[string]: #Node` tail, or an explicit optional `member`-arm), so an in-body resource child is legal | `#DeployValue` stays closed to the LOADER-DERIVED fields (`target`/`member_of`/`descent`) exactly as today; only the authored member child is opened |
| **Loader** (`sdk/loaderkit/node_tree.go` `BuildResourceMemberChildren`, `node_parse`) | classify an in-body entity child as a resource member (already does, given `authoredBodyKeys`); reject a non-resource in-body child | `authoredBodyKeys` already yields the in-body keys; this is the schema opening wiring |
| **Guard** (`sdk/loaderkit` / `candy/plugin-box` validate) | the load-error control (end-state 5) | the load-bearing R2/R3 fix — prevents the class, not the instances |
| **Walk** (`sdk/deploykit/deploy_tree.go`, `sdk/loaderkit/deploy_load.go`) | descend in-substrate, root deploy-level — the ONE walk | already correct; keep it the ONLY reader of position |
| **Migrate** (`candy/plugin-migrate` `reshape_group_deploy.go`) | a group unroll places unrolled members **in-body** (in-substrate) | so `charly migrate` never re-introduces the sibling-for-inside shape |
| **Beds** (`charly/charly.yml`, `distro-arch`, `distro-cachyos`) | each "inside" member moves into its parent body; each bed asserts the GUEST-side marker | the assertion FAILS if the effect lands on the host |

## Migration & landing order (producer-first)

1. **`spec`** — open the kind bodies in the CUE schema; `charly task cue-gen`; ship the regenerated types. (Producer.)
2. **`sdk`** — loader wiring + the walk/guard; unit tests: an in-body member loads and classifies `in-substrate`; a deploy-level `local:` with no `host:` beside a `vm:` FAILS. (Consumer of spec.)
3. **`plugin-migrate`** — unroll into the body.
4. **`charly` + `distro-arch` + `distro-cachyos`** — move the members in-body; re-pin.
5. **docs** — the gated pages (`start/quickstart.md`, `concepts/02`, `concepts/12`, `guides/containers-and-nesting.md`) follow the shipped behaviour, not before.

## Acceptance / R10

- **Unit (fails without the change):** `TestParseFold` — an authored in-body resource
  child classifies `PositionInSubstrate`; the guard test errors on a sibling
  `local:` (no `host:`) beside a `vm:`.
- **Live (the class gate):** every affected bed runs its OWN disposable `check run`
  and asserts the **guest-side** marker — a regression to the sibling shape makes
  the marker land on the host and the bed FAIL. One `kind:check-roster` roster over
  the affected beds is the R10 gate.
- **Zero warnings** on the roster; `charly box validate` 0/0 incl. the new guard.

## What this is NOT

- Not a per-bed workaround (each bed restructured ad hoc) — that leaves the
  authoring gap and the class alive.
- Not a `nested:` field, a `host:` venue-kind, or any second placement spelling —
  those are the parallel paths the member-tree cutover already deleted.
- Not a core kind-switch — the descent stays a plugin-declared trait.
