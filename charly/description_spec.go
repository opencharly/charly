package main

// The unified `plan:` schema — ONE flat ordered list of intent-typed steps that
// is the candy's complete operational + acceptance spec. See the plan file
// "Unify the entire test/eval/benchmark system into ONE flat plan: vocabulary".
//
// A Step carries exactly one intent keyword (run/check/agent-run/agent-check) OR
// an include: directive, plus an inline Op (the verb + matchers + modifiers):
//   - run:         deterministic state-change (the install timeline; build/deploy/provision)
//   - check:       deterministic idempotent probe (verification; safe to run any time)
//   - agent-run:   agent instruction that MAY mutate (graded)
//   - agent-check: agent read-only assessment (graded)
//   - include:     splice another entity's plan steps (composition; <kind>:<name>)
//
// The keyword's value carries the step's prose; the embedded Op carries the verb,
// matchers, and modifiers (id, tag, context, pod, depends_on, count, ...).

import (
	"github.com/opencharly/sdk/kit"
)

// The keyword constant VALUES live in kit (the importable host-engine shared with
// out-of-tree plugin candies — R3); these are the in-core aliases. The StepKeyword TYPE is a
// spec type (aliased via vmshared.StepKeyword).
const (
	KwRun        = kit.KwRun
	KwCheck      = kit.KwCheck
	KwAgentRun   = kit.KwAgentRun
	KwAgentCheck = kit.KwAgentCheck
	KwInclude    = kit.KwInclude
)

// StepKind / keywordsSet / KeywordText / IsAgent / IsInclude / Mutates are methods on the
// spec.Step type (union_types.go + charly_methods.go). The keyword→do-mode dispatch
// (StepDoMode) and the stable step-id derivation (StepID / EffectiveStepID) moved to sdk/kit
// (planspec.go) with the plan walk that consumes them; charly/kit_aliases.go binds what core
// still calls (stepDoMode / EffectiveStepID). The tag-set helpers + tag-expression grammar
// (kit.EffectiveTags / kit.TagExpr / kit.ParseTagExpr) likewise live once in kit.
