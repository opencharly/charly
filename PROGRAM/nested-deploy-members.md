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

`pn.Body` still carries the member key — it IS the position channel the derivation reads — so once
the member is authored in-body its key sits **inside** the kind value, and the closed schema at the
base of every per-kind value refuses it (`#DeployValue: #Deploy & {nested?: _|_, peer?: _|_, …}`,
`spec/schema/node.cue:130`). Measured on `check-group` with its member re-indented into the `vm:`
body:

```
node "check-group": vm: 5 errors in empty disjunction:
    "check-group-member": field not allowed:   (node check-group:1:2)
```

**Without the parse fix there is no member child at all**, so the emitted body is
identical to the authored one and the gate substitution is a byte-for-byte no-op.
**Without the gate strip there is a member child**, but the gate still sees its key and
refuses the node.

### What the corpus measured — the position authors actually got

Measured at **`origin/main`** over the five beds this thread cited. In every one the member sits at
the **same indent as its kind key** — beside it, not inside it — while its own comment claims the
opposite. The line numbers and indents below are the command's own output, unedited:

```
$ git show origin/main:charly.yml > /tmp/main-charly.yml
$ R() { awk -v s=$1 -v e=$2 'NR>=s && NR<=e {match($0,/^ */); i=RLENGTH; t=substr($0,i+1);
      if (t !~ /^#/ && t != "") printf "%d| %d| %s\n", NR, i, substr(t,1,44)}' /tmp/main-charly.yml; }
$ R 1323 1333; R 1372 1385; R 3896 3916; R 1436 1442; R 2067 2081
1323| 0| check-builder-vm:
1324| 4| vm:
1325| 8| from: eval-vm
1326| 8| disposable: true
1328| 8| cpu: 2
1329| 8| ram: 2G
1330| 8| lifecycle: dev
1331| 8| description: >-
1332| 12| Sole proof of the builder DEPLOY leg (runVen
1333| 4| check-builder-member:
1372| 0| check-group:
1373| 4| vm:
1374| 8| from: eval-vm
1375| 8| disposable: true
1377| 8| cpu: 2
1378| 8| ram: 2G
1379| 8| lifecycle: dev
1380| 8| description: >-
1381| 12| R10 witness for the POST-MIGRATE member-tree
1385| 4| check-group-member:
3896| 0| check-kind-host-vm:
3897| 4| vm:
3898| 8| from: eval-vm
3899| 8| disposable: true
3900| 8| cpu: 2
3901| 8| ram: 2G
3902| 8| lifecycle: dev
3903| 8| description: >-
3904| 12| R10 witness for the Phase 5 kind-host profil
3905| 12| eval-vm guest (off the operator's workstatio
3906| 12| under this vm node applies the profile (`fro
3907| 12| via kit.NestedExecutor — the eval-vm canonic
3908| 12| a vm node deploys into the guest, never the 
3909| 12| packages (kind + the per-engine kind-engine-
3910| 12| kubectl/helm) land in the guest, and the mem
3916| 4| check-kind-host-member:
1436| 4| check-structkind-vm:
1437| 8| vm:
1438| 12| from: eval-vm
1442| 8| check-structkind-member:
2067| 4| nested-check-vm:
2068| 8| vm:
2069| 12| agent_provisioned: true
2070| 12| plan:
2071| 16| - check: the VM has booted and is reachable 
2072| 18| exit_status: 0
2073| 18| command: "uptime"
2074| 16| - check: the guest has hardware-accelerated 
2075| 18| file:
2076| 20| file: /dev/kvm
2077| 20| exists: true
2078| 16| - check: the VM guest can reach the outermos
2079| 18| stdout: {contains: [PONG]}
2080| 18| command: "redis-cli -h charly-redis ping"
2081| 8| inner-app-pod:
```

- `check-builder-member`, `check-group-member` and `check-kind-host-member`: member at indent 4, the
  same as their `vm:` key; the node itself at indent 0.
- `check-structkind-member` — **`charly.yml:1442`**, its `vm:` key at `:1437` and the node
  `check-structkind-vm` at `:1436`: member at indent 8, the same as `vm:`. (The thread cites this
  entry at `:1445`; the measured line of the member key itself is `:1442` — the same entry, and the
  indent above is what decides the position.)
- `inner-app-pod` — `:2081`, its `vm:` key at `:2068`, the node `nested-check-vm` at `:2067`:
  member at indent 8, the same as `vm:`.

**That claim is confined to those five beds; it is not a corpus-wide negative.** What a bed does
under the gate depends on its authored shape and, separately, on whether its kind is gated at all,
so both are stated per bed:

- **As shipped at `origin/main` all five are alongside**, and under the `origin/main` gate each of
  them passes — `check-structkind-member` and `inner-app-pod` among them (the run below was taken
  with the three `vm` beds already re-nested in the `charly` leg's tree; those two were not
  re-nested, so their lines in it are their shipped shape).
- **`check-structkind-member` is additionally ungated.** Its disc `examplestructkind` is absent from
  `spec.KindValueDefs`, and the gate returns early for any disc not in that table
  (`charly/charly/provider_kind_invoke.go:416`, `if !ok { return nil }`; the table covers the five
  substrate kinds plus `candy`, `:400-403`). It would not be refused even if it were re-nested — a
  second, independent reason on top of the position.
- **`inner-app-pod`'s disc `pod` IS in that table**, so its pass is the position's doing and not an
  ungated kind's. The three `vm` beds are gated in exactly the same way: they fail in the run below
  solely because they are the three that were re-nested in-body.

**The gate's shape-dependence is measured, not inferred.** With the corpus beds re-nested in-body
and the `origin/main` gate restored, the corpus gate test fails on **exactly those three beds and no
other** — every remaining bed in the corpus passes:

```
$ go test ./ -run TestCueKinds_Corpus -count=1     # with provider_kind_invoke.go at origin/main
    cue_kinds_corpus_test.go:194: FAIL ../charly.yml:vm.check-builder-vm: vm: 5 errors in empty disjunction:
    cue_kinds_corpus_test.go:194: FAIL ../charly.yml:vm.check-group: vm: 5 errors in empty disjunction:
    cue_kinds_corpus_test.go:194: FAIL ../charly.yml:vm.check-kind-host-vm: vm: 5 errors in empty disjunction:
FAIL	github.com/opencharly/charly/charly	37.599s

$ go test ./ -run TestCueKinds_Corpus -count=1     # in the charly leg's tree (branch feat/nested-members-748, gate fix 41d59819;
#  sdk parse fix wired by the local replace)
ok  	github.com/opencharly/charly/charly	37.904s
```

So the closed `#<Kind>Value` arm refuses a key **inside the kind value** and not one beside it: an
alongside member sits outside the value being validated, which is why every other bed passes while
the three re-nested ones fail. That is the whole ordering — the parse fix turns the in-body key into
a member child, and the gate fix then strips it before validation.

**So an author who wants the *into-the-venue* position is left with a spelling the gate refuses**,
and the spelling that ships is the **alongside** one — walked as its **own root**
(`sdk/deploykit/deploy_tree.go:43` walks only the root's `InSubstrateMembers()` into the parent
venue), landing on the **host**, not in the guest: the exact behaviour `30cabf27` (`#79`) moved the
beds into VMs to prevent.

**The `charly` leg (`feat/nested-members-748`, commit `ed499734`) re-nests three of those beds
in-body** — `check-builder-member`, `check-group-member`, `check-kind-host-member`, each from
indent 4 to 8 — which is the position their own comments always claimed.

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
