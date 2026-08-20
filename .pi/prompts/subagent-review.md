---
description: Spawn a fresh-context reviewer sub-agent for adversarial review
argument-hint: "<review-angle>"
---
# Sub-agent Review: $1

## Task for the reviewer

Review the current diff / plan / implementation with a focus on $1.

Use a fresh-context sub-agent:

```workflowScript
subagent({
  agent: "reviewer",
  task: "Review the current diff and implementation in this repo. Focus on: $1. Return concise evidence-backed findings with file/line references. Do not modify files.",
  context: "fresh",
  output: false,
})
```

## After review

- Synthesize findings from all reviewers
- Apply fixes worth doing now
- Defer optional improvements