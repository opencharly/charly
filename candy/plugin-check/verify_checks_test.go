package check

import (
	"slices"
	"testing"

	"github.com/opencharly/spec/spec"
)

// verify_checks_test.go — filterOpsByID unit coverage (K-wave W3a A9): the plugin-side port of
// charly/unified_targets.go's former core-side OnlyIDs pre-filter loop.

func opIDs(ops []spec.Op) []string {
	ids := make([]string, len(ops))
	for i, op := range ops {
		ids[i] = op.ID
	}
	return ids
}

func TestFilterOpsByID(t *testing.T) {
	ops := []spec.Op{{ID: "a"}, {ID: "b"}, {ID: "c"}}

	t.Run("nil onlyIDs is a no-op", func(t *testing.T) {
		got := filterOpsByID(ops, nil)
		if !slices.Equal(opIDs(got), []string{"a", "b", "c"}) {
			t.Fatalf("filterOpsByID(ops, nil) = %v, want all ops unchanged", opIDs(got))
		}
	})

	t.Run("empty onlyIDs is a no-op", func(t *testing.T) {
		got := filterOpsByID(ops, []string{})
		if !slices.Equal(opIDs(got), []string{"a", "b", "c"}) {
			t.Fatalf("filterOpsByID(ops, []) = %v, want all ops unchanged", opIDs(got))
		}
	})

	t.Run("subsets to the listed IDs, preserving ops order", func(t *testing.T) {
		got := filterOpsByID(ops, []string{"c", "a"})
		if !slices.Equal(opIDs(got), []string{"a", "c"}) {
			t.Fatalf("filterOpsByID(ops, [c,a]) = %v, want [a c] (ops order, not onlyIDs order)", opIDs(got))
		}
	})

	t.Run("an unknown ID matches nothing", func(t *testing.T) {
		got := filterOpsByID(ops, []string{"nope"})
		if len(got) != 0 {
			t.Fatalf("filterOpsByID(ops, [nope]) = %v, want empty", opIDs(got))
		}
	})
}
