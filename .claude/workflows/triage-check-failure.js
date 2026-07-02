export const meta = {
  name: 'triage-check-failure',
  description:
    'Competing-hypotheses RCA of a FAILED kind:check bed run (CLAUDE.md R1). Fans out N independent root-cause hypotheses, validates EACH on the live disposable bed, cross-checks them adversarially, converges on the surviving root cause, and returns a concrete fix to apply before re-running the real bed. Use after `charly check run <bed>` exits non-zero. Read-mostly probing on the bed; never edits source or commits.',
  phases: [
    { title: 'Reproduce', detail: 'inspect the failing bed run + summary.yml/logs' },
    { title: 'Hypothesize', detail: 'N independent root-cause theories, each bed-validated' },
    { title: 'Converge', detail: 'adversarial cross-check -> surviving root cause + fix' },
  ],
}

// Bed name to triage. Required. `args` may arrive as an actual array
// (Workflow tool), a JSON-encoded string of that array (tool-call
// stringification), or a space-separated string (slash invocation).
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
let bed = ''
if (Array.isArray(rawArgs)) bed = rawArgs.map(String).map((s) => s.trim()).filter(Boolean)[0] || ''
else if (typeof rawArgs === 'string') bed = rawArgs.trim().split(/\s+/)[0] || ''
if (!bed) {
  log('Usage: /triage-check-failure <bed> — no bed name supplied.')
  return { error: 'no bed supplied' }
}

const REPRO_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    failingStep: { type: 'string' },
    exitCode: { type: 'integer', description: '0 pass / 1 infra / 2 checks-failed' },
    logTail: { type: 'string' },
    observed: { type: 'string', description: 'the concrete failure symptom' },
  },
  required: ['failingStep', 'observed'],
}

const HYPOTHESIS_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    theory: { type: 'string', description: 'the proposed root cause' },
    evidence: { type: 'string', description: 'what was checked on the live bed and what it showed' },
    bedValidated: { type: 'boolean', description: 'true only if probed against the live bed' },
    proposedFix: { type: 'string' },
  },
  required: ['theory', 'evidence', 'bedValidated'],
}

const VERDICT_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    confirmed: { type: 'boolean', description: 'true if the theory survives the refutation attempt' },
    reasoning: { type: 'string' },
  },
  required: ['confirmed', 'reasoning'],
}

phase('Reproduce')
const repro = await agent(
  `You are an check-failure triager. The kind:check bed "${bed}" failed. Read .check/${bed}/<calver>/summary.yml (newest run dir) and tail the failing step's .log. Report the failing step, the process exit code, the log tail, and the concrete observed symptom. Do NOT mutate anything, do NOT re-run the bed.`,
  { schema: REPRO_SCHEMA, label: `reproduce:${bed}`, phase: 'Reproduce' }
)

phase('Hypothesize')
const N = 4
const hyps = (await parallel(
  Array.from({ length: N }, (_unused, i) => () =>
    agent(
      `You are root-cause hypothesis #${i + 1} for the failing kind:check bed "${bed}". Symptom: ${repro && repro.observed ? repro.observed : '(see .check logs)'}. Form ONE independent root-cause theory DISTINCT from the obvious first guess, then VALIDATE it against the LIVE bed (charly status/logs, charly check live <bed> probes, podman inspect, read the emitted artifact) — set bedValidated=true only if you actually probed the live bed. Propose a concrete fix. Do NOT edit source, do NOT re-run charly check run, do NOT commit.`,
      { schema: HYPOTHESIS_SCHEMA, label: `hyp${i + 1}:${bed}`, phase: 'Hypothesize' }
    )
  )
)).filter(Boolean)

phase('Converge')
const judged = (await parallel(
  hyps.map((h, i) => () =>
    agent(
      `Adversarially REFUTE this root-cause theory for bed "${bed}": "${h.theory}". Evidence offered: ${h.evidence}. Try to disprove it using live-bed probes. Default to confirmed=false if the theory is not backed by live-bed evidence. Return confirmed=true ONLY if it genuinely survives refutation.`,
      { schema: VERDICT_SCHEMA, label: `judge${i + 1}:${bed}`, phase: 'Converge' }
    ).then((v) => ({ ...h, verdict: v }))
  )
)).filter(Boolean)

const survivors = judged.filter((h) => h.bedValidated && h.verdict && h.verdict.confirmed)
log(`triage-check-failure(${bed}): ${hyps.length} hypotheses, ${survivors.length} survived adversarial cross-check.`)

return {
  bed,
  reproduce: repro,
  survivingRootCauses: survivors.map((h) => ({ theory: h.theory, evidence: h.evidence, proposedFix: h.proposedFix })),
  allHypotheses: judged,
  note: survivors.length
    ? 'Apply a surviving fix in the working tree, then re-run `charly check run ' + bed + '` to confirm.'
    : 'No hypothesis survived live-bed validation — gather more evidence (logs, charly check live) before editing.',
}
