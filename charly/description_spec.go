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
	"fmt"

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

// StepKind / keywordsSet / KeywordText / IsAgent / IsInclude / Mutates are now
// methods on the spec.Step type (charly/spec — union_types.go + charly_methods.go),
// reached through the `type Step = spec.Step` alias. Only the keyword→do-mode
// dispatch stays here as a free function (DoMode is a package-main enum).

// stepDoMode maps the step keyword to the internal act/assert/instruct dispatch
// enum (DoMode is a package-main type, so this stays a free function in main).
func stepDoMode(s *Step) DoMode {
	switch {
	case s.Run != "":
		return DoAct
	case s.Check != "":
		return DoAssert
	case s.AgentRun != "", s.AgentCheck != "":
		return DoInstruct
	}
	return DoAssert
}

// StepID returns the stable identifier used for plan-overlay merge lookups,
// depends_on references, and ${STEP_ID} substitution. The author-set Op.ID
// wins; otherwise a deterministic id is derived from origin + position.
func StepID(origin string, stepIdx int) string {
	return fmt.Sprintf("plan:%s:%d", origin, stepIdx)
}

// EffectiveStepID returns the step's author id when set, else a derived id.
func EffectiveStepID(s *Step, origin string, stepIdx int) string {
	if s.ID != "" {
		return s.ID
	}
	return StepID(origin, stepIdx)
}

// The tag-set helpers (EffectiveTags / normalizeTag) and the whole tag-expression
// grammar (TagExpr / ParseTagExpr / CombineTagFilters) live ONCE in sdk/kit — see
// charly/kit_aliases.go for the package-main bindings. A plugin candy filters a plan
// by tag with the SAME grammar the check engine uses.
