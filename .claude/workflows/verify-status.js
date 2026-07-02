export const meta = {
  name: 'verify-status',
  description:
    'Substrate-coverage fan-out for the unified `charly status` surface: for each deployment substrate (pod / vm / local / android) run `charly check run <bed>` to completion (build → check image → deploy → check live → fresh charly update → teardown) on the bed that exercises that substrate, and aggregate a verbatim pass/fail report keyed on the bed\'s `status-shows-*` deploy-scope assertion (the check that proves `charly status --json` reports the right `kind` + nested tree for a live deployment). ALL beds run in PARALLEL via parallel(), bounded by the runtime\'s documented 16-concurrent / 1000-total dynamic-workflow agent ceiling — KVM/libvirt are multi-tenant and podman builds distinct image tags concurrently. A bed skipped for a missing host prereq (libvirt user session for the vm bed, /dev/kvm for the android bed) is logged, never silently dropped.',
  phases: [
    { title: 'Discover', detail: 'emit the substrate→bed map {pod,vm,local,android}' },
    { title: 'Verify', detail: 'charly check run <bed> per substrate; return verbatim verdict incl. the status-shows-* assertion' },
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

const BED_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    bed: { type: 'string' },
    substrate: { type: 'string' },
    exitCode: { type: 'integer', description: '0 pass / 1 infra / 2 checks-failed' },
    ok: { type: 'boolean' },
    skippedPrereq: { type: 'boolean', description: 'true if a host prereq (libvirt/kvm) was missing' },
    statusAssertions: {
      type: 'array',
      description: 'verbatim verdicts, one per status-shows-* deploy-scope check',
      items: {
        type: 'object',
        additionalProperties: false,
        properties: {
          id: { type: 'string' },
          ok: { type: 'boolean' },
          detail: { type: 'string', description: 'the check-live line for this check (verbatim)' },
        },
        required: ['id', 'ok'],
      },
    },
    failingLogTail: { type: 'string' },
  },
  required: ['bed', 'substrate', 'exitCode', 'ok'],
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

// All beds run in PARALLEL. KVM and libvirt are multi-tenant; podman builds
// distinct image tags concurrently. Simultaneity is bounded by the runtime's
// documented 16-concurrent dynamic-workflow agent ceiling, which queues excess.
log(`Verifying ${beds.length} substrate bed(s) in parallel (bounded + queued by the 16-concurrent runtime ceiling).`)

const runBed = (b) => {
  const charlyCmd = b.dir ? `charly -C ${b.dir} check run ${b.bed}` : `charly check run ${b.bed}`
  const checkDir = `${b.dir ? b.dir + '/' : ''}.check/${b.bed}/<calver>/`
  const checkList = b.checks.map((c) => `"${c}"`).join(' and ')
  return agent(
    `You are the check-bed runner verifying the unified \`charly status\` surface for the "${b.substrate}" substrate. Run the kind:check bed "${b.bed}" EXACTLY as \`${charlyCmd}\` — do NOT add any flags (no --no-rebuild/--keep/--on-*; that would shrink the R10 spec, CLAUDE.md R10 flag-override clause). The bed's full R10 sequence (build → check image → deploy → check live → fresh charly update → teardown) runs the deploy-scope check check(s) ${checkList}, which assert that \`charly status --json\` reports the correct substrate kind (and, for android, the declared pod→android nested tree) for the live deployment. Capture stdout/stderr and the process exit code. Read ${checkDir}summary.yml for the per-step verdict, and extract the VERBATIM check-live line for EACH of those checks into statusAssertions. Tail any failing step's .log into failingLogTail. If a required host prereq is missing (libvirt user session for the vm bed, /dev/kvm for the android bed), set skippedPrereq=true and do NOT report it as a pass. Set substrate="${b.substrate}" and bed="${b.bed}". Return the verbatim verdict — never summarize away a failure.`,
    { schema: BED_SCHEMA, label: `status:${b.substrate}:${b.bed}`, phase: 'Verify' }
  )
}

phase('Verify')
const all = (await parallel(beds.map((b) => () => runBed(b)))).filter(Boolean)
const passed = all.filter((r) => r.ok && !r.skippedPrereq)
const failed = all.filter((r) => !r.ok && !r.skippedPrereq)
const skipped = all.filter((r) => r.skippedPrereq)
log(`verify-status: ${passed.length} passed, ${failed.length} failed, ${skipped.length} skipped (missing prereq).`)

return {
  total: all.length,
  passed: passed.map((r) => ({ substrate: r.substrate, bed: r.bed, statusAssertions: r.statusAssertions })),
  failed: failed.map((r) => ({ substrate: r.substrate, bed: r.bed, exitCode: r.exitCode, statusAssertions: r.statusAssertions, failingLogTail: r.failingLogTail })),
  skipped: skipped.map((r) => ({ substrate: r.substrate, bed: r.bed })),
  beds: all,
}
