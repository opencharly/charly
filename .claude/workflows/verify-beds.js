export const meta = {
  name: 'verify-beds',
  description:
    'Full-live-test commit-gate fan-out over the existing kind:check disposable beds, also usable for continuous verification throughout development: run `charly check run <bed>` to completion (build → check image → deploy → check live → fresh charly update → teardown) and aggregate a verbatim pass/fail report. ALL beds run in PARALLEL via parallel(), bounded by the runtime’s documented 16-concurrent / 1000-total dynamic-workflow agent ceiling — KVM/libvirt are multi-tenant and podman builds distinct image tags concurrently. Beds skipped for a missing host prereq (libvirt session for a vm bed, /dev/kvm for android) are logged, never silently dropped.',
  phases: [
    { title: 'Discover', detail: 'enumerate kind:check beds + their target kind' },
    { title: 'Run beds', detail: 'charly check run <bed> per bed; return verbatim verdict' },
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
          target: { type: 'string', description: 'pod | vm | local' },
          dir: { type: 'string', description: "charly project dir of the bed ('' = repo root, 'box/<distro>' = submodule)" },
          tokens: { type: 'array', items: { type: 'string' }, description: 'requires_exclusive host-resource tokens (bed node + members); [] when none' },
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
    exitCode: { type: 'integer', description: '0 pass / 1 infra / 2 checks-failed' },
    ok: { type: 'boolean' },
    skippedPrereq: { type: 'boolean', description: 'true if a host prereq (libvirt/kvm) was missing' },
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
  ? `Read the root charly.yml and every box/*/charly.yml in this project. A check bed is a top-level entity whose substrate node (its single kind key: pod/vm/local/k8s/android/group or a plugin substrate word) carries disposable: true. For each of these bed names: ${requested.join(', ')} — return its substrate kind word, the charly project dir it lives in ('' for the repo root, 'box/<distro>' for a submodule bed), and its requires_exclusive host-resource tokens (from the bed node AND any nested members; [] when none). Return JSON {beds:[{bed,target,dir,tokens}]}. Do NOT run anything; do NOT return beds not in the list.`
  : `Read the root charly.yml and every box/*/charly.yml in this project. A check bed is a top-level entity whose substrate node (its single kind key: pod/vm/local/k8s/android/group or a plugin substrate word) carries disposable: true. Return ALL such beds as JSON {beds:[{bed,target,dir,tokens}]} where target is the substrate kind word, dir is the charly project dir the bed lives in ('' for the repo root, 'box/<distro>' for a submodule bed), and tokens is the bed's requires_exclusive host-resource token list (bed node + nested members; [] when none). Exclude non-disposable deploys (operator profiles). Do NOT run anything.`
const discovered = await agent(discoverPrompt, { schema: DISCOVER_SCHEMA, label: 'discover-beds', phase: 'Discover' })
const beds = (discovered && discovered.beds ? discovered.beds : []).filter(Boolean)

if (!beds.length) {
  log('No kind:check beds discovered — nothing to verify.')
  return { beds: [], note: 'no beds discovered' }
}

// Beds sharing an EXCLUSIVE host-resource token (requires_exclusive, e.g.
// nvidia-gpu) MUST run serially — the arbiter fast-fails a second same-token
// claim by design (CLAUDE.md "per-exclusive-resource-token SERIAL groups").
// Group them into serial CHAINS; everything else runs fully parallel. KVM and
// libvirt are multi-tenant; podman builds distinct image tags concurrently.
// Simultaneity is bounded by the runtime's 16-concurrent agent ceiling.
const chains = []
const chainByToken = new Map()
for (const b of beds) {
  const toks = (b.tokens || []).filter(Boolean)
  let chain = null
  for (const t of toks) if (chainByToken.has(t)) { chain = chainByToken.get(t); break }
  if (!chain) { chain = []; chains.push(chain) }
  chain.push(b)
  for (const t of toks) chainByToken.set(t, chain)
}
const serialCount = chains.filter((c) => c.length > 1).length
log(`Discovered ${beds.length} bed(s): ${chains.length} parallel lane(s), ${serialCount} serialized by an exclusive token.`)

const runBed = (b) => {
  const charlyCmd = b.dir ? `charly -C ${b.dir} check run ${b.bed}` : `charly check run ${b.bed}`
  const checkDir = `${b.dir ? b.dir + '/' : ''}.check/${b.bed}/<calver>/`
  return agent(
    `You are the check-bed runner. Run the kind:check bed "${b.bed}" EXACTLY as \`${charlyCmd}\` — do NOT add any flags (no --no-rebuild/--keep/--on-*; that would shrink the R10 spec, CLAUDE.md R10 flag-override clause). Capture stdout/stderr and the process exit code. Then read ${checkDir}summary.yml for the per-step verdict and tail any failing step's .log. If a required host prereq is missing (libvirt user session for a vm bed, /dev/kvm for the android bed), set skippedPrereq=true and do NOT report it as a pass. Return the verbatim verdict — never summarize away a failure.`,
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
log(`verify-beds: ${passed.length} passed, ${failed.length} failed, ${skipped.length} skipped (missing prereq).`)

return {
  total: all.length,
  passed: passed.map((r) => r.bed),
  failed: failed.map((r) => ({ bed: r.bed, exitCode: r.exitCode, failingLogTail: r.failingLogTail })),
  skipped: skipped.map((r) => r.bed),
  beds: all,
}
