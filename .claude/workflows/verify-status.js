export const meta = {
  name: 'verify-status',
  description:
    'Substrate-coverage PLAN for the unified `charly status` surface. It runs NO beds: every substrate bed (pod check-pod, vm check-k3s-vm, android check-android-emulator-pod, local check-local) is either a LONG bed (vm/android substrate, or measured >=600s) or HOST-LOCAL (check-local applies candies to the operator workstation), and an ephemeral agent() sub-agent cannot own either — force-terminating one orphans a libvirt domain or pod container. So it emits, per substrate, the exact `charly check run <bed>` command, the summary.yml path to read, and the `status-shows-*` deploy-scope assertion that bed proves; the PERSISTENT session owns each run as a run_in_background task. The local substrate bed must run inside the disposable eval VM, never on this host. gateComplete is false by construction. The bed-safety classifier lives in /verify-beds (R3) and is not duplicated here.',
  phases: [
    { title: 'Discover', detail: 'confirm each substrate bed exists in config' },
    { title: 'Plan', detail: 'emit per-substrate command + summary.yml path + status-shows-* assertion; run nothing' },
  ],
}

// The fixed substrate→bed map. Each bed carries `status-shows-*` deploy-scope
// check checks that assert `charly status --json` reports the correct `kind` (and,
// for android, the declared pod→android nested tree) for the live deployment.
// `dir` names the charly project the bed lives in ('' = the superproject root;
// the pod/android beds live in `box/<distro>` submodules) — the runner invokes
// `charly -C <dir> check run <bed>` and reads `<dir>/.check/…`. One bed per
// substrate is the unit of coverage — distinct beds get distinct
// container/VM/image names; the author gives each disjoint host ports (the
// loader does NOT check ports — an overlap fails the second bed at deploy),
// so they run concurrent-safe with no worktree.
const SUBSTRATE_BEDS = [
  { substrate: 'pod', bed: 'check-pod', dir: 'box/fedora', checks: ['status-shows-pod'] },
  { substrate: 'vm', bed: 'check-k3s-vm', dir: '', checks: ['status-shows-vm'] },
  { substrate: 'local', bed: 'check-local', dir: '', checks: ['status-shows-local'] },
  { substrate: 'android', bed: 'check-android-emulator-pod', dir: 'box/cachyos', checks: ['status-shows-android', 'status-shows-nested'] },
]

// Normalize requested substrates/beds. Empty => all four.
// `args` may arrive as an actual array (Workflow tool), a JSON-encoded string
// of that array (tool-call stringification), or a space-separated string
// (slash invocation). Decode JSON first, then normalize.
let rawArgs = args
if (typeof rawArgs === 'string') {
  const t = rawArgs.trim()
  if (t.startsWith('[') || t.startsWith('"')) {
    try {
      rawArgs = JSON.parse(t)
    } catch {
      rawArgs = t
    }
  } else {
    rawArgs = t
  }
}
let requested = []
if (Array.isArray(rawArgs)) requested = rawArgs.map(String).map((s) => s.trim()).filter(Boolean)
else if (typeof rawArgs === 'string' && rawArgs.trim()) requested = rawArgs.trim().split(/\s+/)

let selected = SUBSTRATE_BEDS
if (requested.length) {
  const want = new Set(requested)
  selected = SUBSTRATE_BEDS.filter((e) => want.has(e.substrate) || want.has(e.bed))
}

const DISCOVER_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    beds: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        properties: {
          substrate: { type: 'string', description: 'pod | vm | local | android' },
          bed: { type: 'string' },
          dir: { type: 'string', description: "charly project dir of the bed ('' = repo root)" },
          checks: {
            type: 'array',
            items: { type: 'string' },
            description: 'the status-shows-* deploy-scope assertion ids',
          },
        },
        required: ['substrate', 'bed', 'checks'],
      },
    },
  },
  required: ['beds'],
}


phase('Discover')
// The substrate→bed map is fixed in this workflow; the discover phase only
// CONFIRMS each selected entry still exists in its project's check: block —
// the SELECTED map stays authoritative (the discover agent can drop entries
// it cannot confirm, never add or substitute beds). Do NOT run anything here.
const discoverPrompt =
  `STRICTLY CONFIRM — never add, never substitute — each of these substrate→bed entries against the kind:check beds in their charly project (dir '' or '.' = this repo's charly.yml top-level check: block; otherwise <dir>/charly.yml): ` +
  selected.map((e) => `${e.substrate}=>${e.bed} in '${e.dir || '.'}' (checks: ${e.checks.join(' + ')})`).join(', ') +
  `. Return ONLY the entries whose bed AND every named status-shows-* deploy-scope check exist, verbatim, as JSON {beds:[{substrate,bed,dir,checks}]}. Do NOT run anything; do NOT include any bed not in this list.`
const discovered = await agent(discoverPrompt, { schema: DISCOVER_SCHEMA, label: 'discover-status-beds', phase: 'Discover' })
const confirmed = new Set(
  (discovered && discovered.beds ? discovered.beds : [])
    .filter(Boolean)
    .map((b) => b.substrate + '|' + b.bed)
)
// Code-side intersection — the agent's output can only NARROW the selected
// map, never widen it (an over-helpful discover agent must not fan out
// beyond what the caller asked for).
const beds = selected.filter((e) => confirmed.has(e.substrate + '|' + e.bed))
const unconfirmed = selected.filter((e) => !confirmed.has(e.substrate + '|' + e.bed))
if (unconfirmed.length) {
  log(
    `verify-status: ${unconfirmed.length} selected entr(y/ies) not confirmed in config — skipped: ` +
      unconfirmed.map((e) => `${e.substrate}=>${e.bed}`).join(', ')
  )
}

if (!beds.length) {
  log('No substrate beds resolved — nothing to verify.')
  return { beds: [], note: 'no substrate beds resolved' }
}

// ---------------------------------------------------------------------------
// THIS WORKFLOW RUNS NO BEDS. It PLANS.
//
// An `agent()` sub-agent is EPHEMERAL: it returns synchronously, its background
// children die with it, and its one foreground `charly check run` is Bash-capped at
// 600s. So it cannot own a bed that outlives its turn (/charly-internals:agents:
// "NEVER the sub-agent /verify-beds workflow for >600s beds"). Driving one anyway
// force-terminates the orchestrator: no verdict, and an ORPHANED libvirt domain or
// pod container.
//
// EVERY substrate bed below is disqualified from sub-agent ownership:
//   pod     check-pod                   - measured >=600s
//   vm      check-k3s-vm                - vm substrate: boots a machine
//   android check-android-emulator-pod  - android substrate: boots an emulator
//   local   check-local                 - HOST-LOCAL: applies candies to the operator
//                                          workstation; belongs in a disposable eval VM
// So a RUNNER form of this workflow is invalid by construction. It emits the plan —
// the exact command per substrate and the `status-shows-*` assertion each proves — and
// the PERSISTENT session owns each run as a `run_in_background` task, reading the
// verdict from `<dir>/.check/<bed>/<calver>/summary.yml`.
//
// The bed-safety classifier lives in ONE place, `/verify-beds` (R3): delegate there
// when you want it applied. This workflow never re-implements it.
phase('Plan')
const plan = beds.map((b) => ({
  substrate: b.substrate,
  bed: b.bed,
  dir: b.dir || '',
  cmd: b.dir ? `charly -C ${b.dir} check run ${b.bed}` : `charly check run ${b.bed}`,
  summaryPath: `${b.dir ? b.dir + '/' : ''}.check/${b.bed}/<calver>/summary.yml`,
  proves: b.checks,
  ownedBy: 'persistent-session',
  reason:
    b.substrate === 'local'
      ? 'HOST-LOCAL: applies candies to the operator workstation — run it inside the disposable eval VM, never on this host'
      : 'long bed: a sub-agent cannot own a run that outlives its turn',
}))

for (const e of plan) log(`PLAN ${e.substrate}: ${e.cmd} — proves ${e.proves.join(', ')} (${e.reason})`)
log(`verify-status: planned ${plan.length} substrate bed(s). This workflow ran NONE — gateComplete=false by construction.`)

return {
  total: 0,
  planned: plan,
  ranHere: [],
  gateComplete: false,
  note:
    'PLAN ONLY — no bed was run. Each planned[].cmd must be launched by the PERSISTENT session as a ' +
    'run_in_background task; read planned[].summaryPath for the verdict and confirm planned[].proves. ' +
    'The `local` substrate bed is HOST-LOCAL and must run inside the disposable eval VM, never on this host. ' +
    'The bed-safety classifier lives in /verify-beds (R3); this workflow does not duplicate it.',
}
