package main

// step_topo.go — step-level dependency resolution.
//
// Post the plan-unify cutover the scoring + ordering unit is the STEP. The
// step's depends_on: lists step ids; pod: targets a container — both
// carried on the step's inline Op. One helper:
//
//   - topoSortSteps: orders steps so every step runs after the steps its
//     Op.DependsOn ids name. Ties break by declaration index. *CycleError on
//     a cycle (reusing graph.go's type).

import "sort"

// stepID returns the step's effective id for a given declaration position.
// origin is the plan origin used to derive a stable id when Op.ID is unset.
func stepID(s Step, origin string, idx int) string {
	return EffectiveStepID(&s, origin, idx)
}

// topoSortSteps returns steps in dependency-respecting order. Each step's
// Op.DependsOn entry is treated as an edge (dep-step -> dependent-step), where
// the dep names another step's id (its author Op.ID, or its derived id). Ties
// break by declaration index. Returns *CycleError on a cycle.
func topoSortSteps(steps []Step, origin string) ([]Step, error) {
	if len(steps) == 0 {
		return nil, nil
	}
	idToIdx := make(map[string]int, len(steps))
	for i, s := range steps {
		idToIdx[stepID(s, origin, i)] = i
	}

	indeg := make([]int, len(steps))
	fwd := make([][]int, len(steps))
	for i, s := range steps {
		for _, dep := range s.DependsOn {
			depIdx, ok := idToIdx[dep]
			if !ok {
				continue // dangling ref (validator catches earlier; defensive)
			}
			fwd[depIdx] = append(fwd[depIdx], i)
			indeg[i]++
		}
	}

	ready := make([]int, 0, len(steps))
	for i, n := range indeg {
		if n == 0 {
			ready = append(ready, i)
		}
	}
	sort.Ints(ready)

	out := make([]Step, 0, len(steps))
	for len(ready) > 0 {
		head := ready[0]
		ready = ready[1:]
		out = append(out, steps[head])
		for _, succ := range fwd[head] {
			indeg[succ]--
			if indeg[succ] == 0 {
				ready = append(ready, succ)
				sort.Ints(ready)
			}
		}
	}
	if len(out) != len(steps) {
		remaining := make([]string, 0)
		for i, n := range indeg {
			if n > 0 {
				remaining = append(remaining, stepID(steps[i], origin, i))
			}
		}
		sort.Strings(remaining)
		return nil, &CycleError{Cycle: remaining}
	}
	return out, nil
}

// firstUnmetDepStep returns the first dep id in s.DependsOn whose verdict is
// anything other than "pass" (or that is unknown / not yet run). Returns "" if
// every dep passed (or the step has no deps).
func firstUnmetDepStep(s Step, verdictByID map[string]string) string {
	for _, dep := range s.DependsOn {
		v, ok := verdictByID[dep]
		if !ok || v != "pass" {
			return dep
		}
	}
	return ""
}
