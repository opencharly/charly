package loader

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// validate_preempt_node_test.go — relocated from charly/preempt_schema_test.go (#55
// decoupling, Batch A, cross-batch file-ownership matrix: Batch A executes this move on Batch
// C's behalf). TestValidatePreemptibleOnNode asserts loaderkit.ValidatePreemptibleOnNode
// directly, zero charly dep.
//
// The diagnostic-inspection helpers below are inlined locally (rather than named
// package-level functions) to avoid any name collision with whatever Batch C's own
// plugin-loader test files (covering the SAME ValidatePreemptibleOnNode/ValidatePreemptible
// functions from validate_preempt_test.go) declare in this same package — the two batches
// work in separate worktrees and cannot see each other's additions before merge.

func TestValidatePreemptibleOnNode(t *testing.T) {
	hasErr := func(d spec.Diagnostics) bool {
		for _, it := range d.Items {
			if it.Severity == "error" {
				return true
			}
		}
		return false
	}
	diagText := func(d spec.Diagnostics) string {
		var msgs []string
		for _, it := range d.Items {
			if it.Severity == "error" {
				msgs = append(msgs, it.Message)
			}
		}
		return strings.Join(msgs, "\n")
	}

	cases := []struct {
		name     string
		node     spec.FleetNode
		wantErr  bool
		contains string
	}{
		{
			name: "valid holder",
			node: spec.FleetNode{Preemptible: &spec.PreemptibleConfig{Holds: []string{"gpu"}}},
		},
		{
			name: "valid claimant",
			node: spec.FleetNode{RequiresExclusive: []string{"gpu"}},
		},
		{
			name:     "empty holds",
			node:     spec.FleetNode{Preemptible: &spec.PreemptibleConfig{}},
			wantErr:  true,
			contains: "must list at least one",
		},
		{
			name:     "bad stop",
			node:     spec.FleetNode{Preemptible: &spec.PreemptibleConfig{Holds: []string{"gpu"}, Stop: "pause"}},
			wantErr:  true,
			contains: "not supported",
		},
		{
			name:     "bad restore",
			node:     spec.FleetNode{Preemptible: &spec.PreemptibleConfig{Holds: []string{"gpu"}, Restore: "maybe"}},
			wantErr:  true,
			contains: "is invalid",
		},
		{
			name:     "empty requires token",
			node:     spec.FleetNode{RequiresExclusive: []string{""}},
			wantErr:  true,
			contains: "empty token",
		},
		{
			name: "self-contention",
			node: spec.FleetNode{
				Preemptible:       &spec.PreemptibleConfig{Holds: []string{"gpu"}},
				RequiresExclusive: []string{"gpu"},
			},
			wantErr:  true,
			contains: "cannot both hold and require",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d spec.Diagnostics
			node := tc.node
			loaderkit.ValidatePreemptibleOnNode(tc.name, &node, &d)
			if hasErr(d) != tc.wantErr {
				t.Fatalf("HasErrors=%v want %v (errs=%s)", hasErr(d), tc.wantErr, diagText(d))
			}
			if tc.contains != "" && !strings.Contains(diagText(d), tc.contains) {
				t.Fatalf("error %q does not contain %q", diagText(d), tc.contains)
			}
		})
	}
}
