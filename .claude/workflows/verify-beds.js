export const meta = {
  name: 'verify-beds',
  description:
    'Fan the SHORT kind:check disposable beds out in parallel — `charly check run <bed>` to completion (build → check image → deploy → check live → fresh charly update → teardown) — and aggregate a verbatim pass/fail report. It runs ONLY the beds a sub-agent can own: an agent() sub-agent returns synchronously and its foreground bed run is Bash-capped at 600s, so LONG beds (vm/android substrates, or any bed whose last run took >=600s) are DEFERRED and returned in deferredLongBeds for the PERSISTENT session to own as run_in_background tasks — never force-terminated here, which orphans libvirt domains and pod containers. HOST-LOCAL beds (a local: deploy, or a bed with a local: member, whose host: is local) are REFUSED: they apply candies to the operator workstation and belong in a disposable eval VM. Nothing is silently dropped: deferrals, refusals, and missing-host-prereq skips are all logged and returned, and gateComplete is false whenever the roster is partial.',
  phases: [
    { title: 'Discover', detail: 'enumerate disposable beds + substrate, host:, last duration' },
    { title: 'Run beds', detail: 'short beds only; long + host-local are handed back' },
  ],
}

// Normalize requested beds. Empty => all beds.
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
          bed: { type: 'string' },
          target: { type: 'string', description: 'pod | vm | local | k8s | android | group' },
          dir: { type: 'string', description: "charly project dir of the bed ('' = repo root, 'box/<distro>' = submodule)" },
          tokens: { type: 'array', items: { type: 'string' }, description: 'requires_exclusive host-resource tokens (bed node + members); [] when none' },
          host: { type: 'string', description: "the `host:` FIELD of a local: bed ('local' or absent => it mutates THIS workstation); '' for non-local substrates" },
          lastSeconds: { type: 'number', description: 'total_seconds from the NEWEST .check/<bed>/<calver>/summary.yml, else 0 when never run' },
        },
        required: ['bed', 'target'],
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
    exitCode: { type: 'integer', description: '0 pass / 1 infra / 2 checks-failed / 3 skipped-absent-host-prereq' },
    ok: { type: 'boolean' },
    skippedPrereq: { type: 'boolean', description: 'true if a host prereq was missing — set DETERMINISTICALLY when exitCode===3 (charly emits it + a "SKIPPED" line), or when a libvirt/kvm prereq is otherwise absent' },
    steps: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        properties: {
          name: { type: 'string' },
          ok: { type: 'boolean' },
          durationSeconds: { type: 'number' },
        },
        required: ['name', 'ok'],
      },
    },
    failingLogTail: { type: 'string' },
  },
  required: ['bed', 'exitCode', 'ok'],
}

phase('Discover')
const discoverPrompt = requested.length
  ? `Read the root charly.yml and every box/*/charly.yml in this project. A check bed is a top-level entity whose substrate node (its single kind key: pod/vm/local/k8s/android/group or a plugin substrate word) carries disposable: true. For each of these bed names: ${requested.join(', ')} — return its substrate kind word, the charly project dir it lives in ('' for the repo root, 'box/<distro>' for a submodule bed), and its requires_exclusive host-resource tokens (from the bed node AND any nested members; [] when none). Return JSON {beds:[{bed,target,dir,tokens,host,lastSeconds}]}. Also return, for each bed: 'host' — set it to 'local' when the bed ITSELF, or ANY nested member of it, is a local: deploy whose host: field is 'local' or absent (i.e. it applies candies to THIS workstation); set it to the remote spec when a local: deploy names a remote host:; otherwise '' — and 'lastSeconds', the total_seconds value from the NEWEST <dir>/.check/<bed>/<calver>/summary.yml on disk (0 when the bed has never run). Read those summary files; do NOT run anything. Do NOT return beds not in the list.`
  : `Read the root charly.yml and every box/*/charly.yml in this project. A check bed is a top-level entity whose substrate node (its single kind key: pod/vm/local/k8s/android/group or a plugin substrate word) carries disposable: true. Return ALL such beds as JSON {beds:[{bed,target,dir,tokens,host,lastSeconds}]} where target is the substrate kind word, dir is the charly project dir the bed lives in ('' for the repo root, 'box/<distro>' for a submodule bed), and tokens is the bed's requires_exclusive host-resource token list (bed node + nested members; [] when none). Exclude non-disposable deploys (operator profiles). Also return, for each bed: 'host' — set it to 'local' when the bed ITSELF, or ANY nested member of it, is a local: deploy whose host: field is 'local' or absent (i.e. it applies candies to THIS workstation); set it to the remote spec when a local: deploy names a remote host:; otherwise '' — and 'lastSeconds', the total_seconds value from the NEWEST <dir>/.check/<bed>/<calver>/summary.yml on disk (0 when the bed has never run). Read those summary files; do NOT run anything.`
const discovered = await agent(discoverPrompt, { schema: DISCOVER_SCHEMA, label: 'discover-beds', phase: 'Discover' })
const beds = (discovered && discovered.beds ? discovered.beds : []).filter(Boolean)

if (!beds.length) {
  log('No kind:check beds discovered — nothing to verify.')
  return { beds: [], note: 'no beds discovered' }
}

// ---------------------------------------------------------------------------
// PARTITION — what this workflow may run, and what it must hand back.
//
// An `agent()` sub-agent is EPHEMERAL: it returns synchronously, its background
// children die with it, and its single foreground `charly check run` is capped by
// the Bash tool (600s max). So a sub-agent CANNOT own a bed that outlives its turn
// — /charly-internals:agents: "NEVER the sub-agent /verify-beds workflow for >600s
// beds." Driving one anyway force-terminates the orchestrator mid-run: no verdict is
// written, and the libvirt domain (owned by virtqemud) or pod container (owned by
// conmon) is ORPHANED. Not a hypothesis — it is what happened: three running domains
// and four containers left behind, and one bed that had actually PASSED recorded as
// a failure because its agent was killed before the orchestrator wrote summary.yml.
//
// So beds split three ways, and nothing is silently dropped (no silent caps):
//   LONG        -> NOT run here. Returned for the PERSISTENT session to own as
//                  `run_in_background` tasks — the only owner that survives across
//                  turns to receive each completion notification.
//   HOST-LOCAL  -> NOT run here. A `local:` bed (or one with a local: member) whose
//                  `host:` is `local` applies candies to THIS workstation. Test beds
//                  belong in a disposable eval VM, never on the operator's machine.
//   SHORT       -> run here, in parallel, exactly as before.
const LONG_BED_SECONDS = 600 // the Bash foreground cap; at or above it a sub-agent cannot finish the bed
const LONG_SUBSTRATES = new Set(['vm', 'android']) // always boot a machine/emulator: minutes, never seconds

const cmdFor = (b) => (b.dir ? `charly -C ${b.dir} check run ${b.bed}` : `charly check run ${b.bed}`)
const isHostLocal = (b) => (b.host || '') === 'local'
const isLong = (b) => LONG_SUBSTRATES.has(b.target) || Number(b.lastSeconds || 0) >= LONG_BED_SECONDS
const longReason = (b) =>
  LONG_SUBSTRATES.has(b.target)
    ? `substrate '${b.target}' always boots a machine (minutes)`
    : `last run took ${Math.round(Number(b.lastSeconds))}s >= ${LONG_BED_SECONDS}s`

const hostLocalBeds = beds.filter(isHostLocal)
const runnable = beds.filter((b) => !isHostLocal(b))
const longBeds = runnable.filter(isLong)
const shortBeds = runnable.filter((b) => !isLong(b))

for (const b of hostLocalBeds) log(`REFUSED (host-local): ${b.bed} — applies candies to THIS workstation; run it in the eval VM.`)
for (const b of longBeds) log(`DEFERRED (long): ${b.bed} — ${longReason(b)}; the persistent session must own it as a background task.`)

const deferredLongBeds = longBeds.map((b) => ({ bed: b.bed, cmd: cmdFor(b), reason: longReason(b) }))
const refusedHostLocalBeds = hostLocalBeds.map((b) => ({ bed: b.bed, cmd: cmdFor(b), reason: 'host-local: applies candies to the operator workstation' }))

if (!shortBeds.length) {
  log(`No sub-agent-runnable bed: ${longBeds.length} deferred, ${hostLocalBeds.length} refused.`)
  return { total: 0, passed: [], failed: [], skipped: [], deferredLongBeds, refusedHostLocalBeds, gateComplete: false, note: 'nothing runnable in a sub-agent; see deferredLongBeds + refusedHostLocalBeds' }
}

// Beds sharing an EXCLUSIVE host-resource token (requires_exclusive, e.g.
// nvidia-gpu) MUST run serially — the arbiter fast-fails a second same-token
// claim by design (CLAUDE.md "per-exclusive-resource-token SERIAL groups").
// Group them into serial CHAINS; everything else runs fully parallel. KVM and
// libvirt are multi-tenant; podman builds distinct image tags concurrently.
// Simultaneity is bounded by the runtime's 16-concurrent agent ceiling.
const chains = []
const chainByToken = new Map()
for (const b of shortBeds) {
  const toks = (b.tokens || []).filter(Boolean)
  let chain = null
  for (const t of toks) if (chainByToken.has(t)) { chain = chainByToken.get(t); break }
  if (!chain) { chain = []; chains.push(chain) }
  chain.push(b)
  for (const t of toks) chainByToken.set(t, chain)
}
const serialCount = chains.filter((c) => c.length > 1).length
log(`${beds.length} bed(s) discovered: ${shortBeds.length} runnable here (${chains.length} lane(s), ${serialCount} token-serialized), ${longBeds.length} deferred to the persistent session, ${hostLocalBeds.length} refused as host-local.`)

const runBed = (b) => {
  const charlyCmd = b.dir ? `charly -C ${b.dir} check run ${b.bed}` : `charly check run ${b.bed}`
  const checkDir = `${b.dir ? b.dir + '/' : ''}.check/${b.bed}/<calver>/`
  return agent(
    `You are the check-bed runner. Run the kind:check bed "${b.bed}" EXACTLY as \`${charlyCmd}\` — do NOT add any flags (no --no-rebuild/--keep/--on-*; that would shrink the R10 spec, CLAUDE.md R10 flag-override clause). Capture stdout/stderr and the process exit code. Then read ${checkDir}summary.yml for the per-step verdict and tail any failing step's .log. If the process exit code is 3, charly itself skipped the bed for an ABSENT HOST PREREQ (it prints a "SKIPPED — …" line, e.g. a GPU resource whose vendor has no matching card): set skippedPrereq=true, ok=false, and record its reason — this is DETERMINISTIC, not your inference. Also set skippedPrereq=true if a required host prereq is otherwise missing (libvirt user session for a vm bed, /dev/kvm for the android bed) and do NOT report it as a pass. Return the verbatim verdict — never summarize away a failure.`,
    { schema: BED_SCHEMA, label: `bed:${b.bed}`, phase: 'Run beds' }
  )
}

phase('Run beds')
const chainResults = await parallel(chains.map((chain) => async () => {
  const out = []
  for (const b of chain) out.push(await runBed(b))   // serial within a token chain
  return out
}))
const all = chainResults.flat().filter(Boolean)
const passed = all.filter((r) => r.ok && !r.skippedPrereq)
const failed = all.filter((r) => !r.ok && !r.skippedPrereq)
const skipped = all.filter((r) => r.skippedPrereq)
log(`verify-beds: ${passed.length} passed, ${failed.length} failed, ${skipped.length} skipped (missing prereq), ${deferredLongBeds.length} deferred, ${refusedHostLocalBeds.length} refused.`)

// A roster is NOT green until the deferred long beds have been run BY THE PERSISTENT
// SESSION and their verdicts read. This result set is partial by construction — say so.
return {
  total: all.length,
  passed: passed.map((r) => r.bed),
  failed: failed.map((r) => ({ bed: r.bed, exitCode: r.exitCode, failingLogTail: r.failingLogTail })),
  skipped: skipped.map((r) => r.bed),
  beds: all,
  deferredLongBeds,
  refusedHostLocalBeds,
  gateComplete: deferredLongBeds.length === 0 && refusedHostLocalBeds.length === 0,
  note:
    deferredLongBeds.length || refusedHostLocalBeds.length
      ? 'PARTIAL: this is NOT a complete R10 roster. Run each deferredLongBeds[].cmd from the PERSISTENT session as a run_in_background task and read its .check/<bed>/<calver>/summary.yml; refusedHostLocalBeds must run inside the disposable eval VM, never on this host.'
      : 'complete: every discovered bed ran here',
}
