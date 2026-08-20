---
description: Spawn a sub-agent to run a charly bed and report results
argument-hint: "<bed-name>"
---
# Sub-agent Verify: $1

## Task for the verifier

Run `charly check run $1` to completion and report verbatim output.

Use a fresh-context sub-agent:

```workflowScript
subagent({
  agent: "worker",
  task: "Run \`charly check run $1\` from the project root. Report the full output including all PASS/FAIL/SKIP lines, the summary line, and any errors. Do not modify any files.",
  context: "fresh",
  async: true,
})
```

## After verification

- Collect the report
- If the bed failed, investigate and fix before landing