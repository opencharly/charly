package main

// step_topo.go — the step-level dependency helper the check runner uses to
// short-circuit a step whose declared deps have not passed.
//
// (The topological sort + the list-structure/depends_on plan validator that used to
// live alongside it were dead — no caller in the tree — and were deleted in P12. Only
// the live helper below remains; it moves into candy/plugin-check with its sole caller,
// check_runner_live.go, in the check-family externalization.)

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
