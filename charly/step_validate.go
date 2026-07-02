package main

// step_validate.go — shared validator for any plan step list, regardless
// of whether it came from a candy/box/pod/vm description or a deploy overlay.
//
// Post the plan-unify cutover the unit is the STEP. Callers parametrize the
// rule set via PlanValidationContext.

import (
	"errors"
	"fmt"
	"strings"
)

// PlanValidationContext parametrizes ValidatePlan for the caller's rule set +
// error framing.
type PlanValidationContext struct {
	// OwnerLabel is the prefix used in error messages, e.g.
	//   "candy redis" / "box fedora-coder" / "iterate plan redis-bench".
	OwnerLabel string

	// RequirePod toggles enforcement of a non-empty Op.Venue on check/agent-check
	// steps. True for scored iterate plans (where the venue IS the scoring
	// target); false for candy/box descriptions (which run against the entity
	// itself). The venue is loader-derived from each step's bundle-tree position
	// (flattenBundleVenues), so this rule runs on the FLATTENED plan — a scored
	// step with no resolvable tree position is the error it catches.
	RequirePod bool
}

// ValidatePlan runs every rule that applies to ANY plan step list:
//
//   - step-id uniqueness (depends_on resolution requires it)
//   - pod field present on check/agent-check steps (when RequirePod is true)
//   - depends_on entries resolve intra-plan (no cross-plan refs)
//   - depends_on graph is acyclic (uses topoSortSteps)
//
// All error messages are prefixed with ctx.OwnerLabel.
func ValidatePlan(plan []Step, ctx PlanValidationContext) error {
	if len(plan) == 0 {
		return nil
	}
	origin := ctx.OwnerLabel

	// Pass 1: id uniqueness + pod requirement.
	known := make(map[string]bool, len(plan))
	ids := make([]string, 0, len(plan))
	for i, s := range plan {
		id := stepID(s, origin, i)
		if known[id] {
			return fmt.Errorf("%s: duplicate step id %q (each step id must be unique for depends_on resolution)",
				ctx.OwnerLabel, id)
		}
		known[id] = true
		ids = append(ids, id)

		if ctx.RequirePod && (s.Check != "" || s.AgentCheck != "") && s.Venue == "" {
			return fmt.Errorf("%s: step %q: no execution venue resolved from tree position — every scored check step in an iterate plan must live under an agent-provisioned node (or a member/nested resource) so its venue is derived from position",
				ctx.OwnerLabel, id)
		}
	}

	// Pass 2: depends_on resolves intra-plan.
	for i, s := range plan {
		id := stepID(s, origin, i)
		for _, dep := range s.DependsOn {
			if dep == id {
				return fmt.Errorf("%s: step %q: depends_on cannot reference itself (%q)",
					ctx.OwnerLabel, id, dep)
			}
			if !known[dep] {
				suggestion := findSimilarName(dep, ids)
				if suggestion != "" {
					return fmt.Errorf("%s: step %q: depends_on: unknown step id %q (did you mean %q?) — depends_on resolution is intra-plan",
						ctx.OwnerLabel, id, dep, suggestion)
				}
				return fmt.Errorf("%s: step %q: depends_on: unknown step id %q — depends_on resolution is intra-plan (available ids: %s)",
					ctx.OwnerLabel, id, dep, strings.Join(ids, ", "))
			}
		}
	}

	// Pass 3: cycle detection via the shared topo-sort.
	if _, err := topoSortSteps(plan, origin); err != nil {
		if cycleErr, ok := errors.AsType[*CycleError](err); ok {
			return fmt.Errorf("%s: step depends_on cycle: %s",
				ctx.OwnerLabel, strings.Join(cycleErr.Cycle, " -> "))
		}
		return fmt.Errorf("%s: depends_on resolution failed: %w", ctx.OwnerLabel, err)
	}
	return nil
}
