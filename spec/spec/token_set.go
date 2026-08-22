package spec

import "strings"

// token_set.go — the ONE resource-arbiter token-list normalizer (R3 consolidation, K-wave 2 cone
// R2). Six byte-identical copies existed before this file: charly/preempt.go, candy/plugin-preempt/
// arbiter_support.go, candy/plugin-check/bed_session.go, candy/plugin-vm/vm_util_shims.go, and
// sdk/loaderkit/validate_capabilities.go (as preemptDedupeNonEmpty). Package spec is the single
// home every one of them already imports — charly core (spec-only import purity), every candy, and
// every sdk kit — so it is the only placement that can serve all six without a new dependency edge.
// It sits beside arbiter_consts.go because the lists it normalizes are the arbiter's
// holds:/requires_exclusive:/requires_shared: token vocabulary.

// DedupeNonEmpty trims each entry and returns the input in order with blanks and repeats removed.
// Returns nil for an input that yields no entries (a nil-vs-empty distinction every caller relies
// on for its "no tokens declared" early return).
func DedupeNonEmpty(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
