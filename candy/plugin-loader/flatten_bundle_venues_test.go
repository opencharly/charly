package loader

import (
	"testing"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// flatten_bundle_venues_test.go — relocated from charly/node_bundle_venue_test.go (#55
// decoupling, Batch A, cross-batch file-ownership matrix: Batch A executes this move on Batch
// C's behalf). Both tests assert loaderkit.FlattenBundleVenues directly, zero charly dep.
// (charly's cmdOp desugared-Op fixture helper is inlined here as a literal — see the
// "test -f /done" step below — rather than ported, since it is used exactly once.)

// TestFlattenBundleVenues_StampsAndHoists verifies the loader venue pass:
// member steps get a bare venue, nested-child steps a dotted venue, and all are
// hoisted into the root bundle's flat Plan (member/child Plans cleared).
func TestFlattenBundleVenues_StampsAndHoists(t *testing.T) {
	uf := &spec.UnifiedFile{Bundle: map[string]spec.BundleNode{
		// A pure-GROUP bed whose agent-provisioned member `os` carries a step.
		"default": {
			Target: "", // group
			Members: map[string]*spec.BundleNode{
				"os": {
					Target:           "pod",
					AgentProvisioned: true,
					Plan: []spec.Step{
						{Check: "marker present", Op: spec.Op{Plugin: "file", PluginInput: map[string]any{"file": "/etc/charly-os-marker"}}},
					},
				},
			},
		},
		// A WORKLOAD bed (own container) with a direct step AND a nested child.
		"cross": {
			Target: "pod",
			Image:  "web",
			Plan: []spec.Step{
				{Check: "web serves marker", Op: spec.Op{Plugin: "http", PluginInput: map[string]any{"http": "http://127.0.0.1:8080/"}}},
			},
			Children: map[string]*spec.BundleNode{
				"migrate": {
					Target:           "pod",
					AgentProvisioned: true,
					Plan: []spec.Step{
						{Check: "migration ran", Op: spec.Op{Plugin: "command", PluginInput: map[string]any{"command": "test -f /done"}}},
					},
				},
			},
		},
	}}

	if err := loaderkit.FlattenBundleVenues(uf); err != nil {
		t.Fatalf("flattenBundleVenues: %v", err)
	}

	// default: one step hoisted, venue == bare member name "os".
	def := uf.Bundle["default"]
	if len(def.Plan) != 1 {
		t.Fatalf("default: want 1 hoisted step, got %d", len(def.Plan))
	}
	if def.Plan[0].Venue != "os" {
		t.Errorf("default member step venue = %q, want %q", def.Plan[0].Venue, "os")
	}
	if p := def.Members["os"].Plan; len(p) != 0 {
		t.Errorf("default member os.Plan should be cleared after hoist, got %d steps", len(p))
	}

	// cross: root step venue == "cross"; nested-child step venue == "cross.migrate".
	cross := uf.Bundle["cross"]
	if len(cross.Plan) != 2 {
		t.Fatalf("cross: want 2 steps (root + hoisted child), got %d", len(cross.Plan))
	}
	venues := map[string]bool{}
	for _, s := range cross.Plan {
		venues[s.Venue] = true
	}
	if !venues["cross"] {
		t.Errorf("cross: missing root-venue step (venue %q); got venues %v", "cross", venues)
	}
	if !venues["cross.migrate"] {
		t.Errorf("cross: missing nested-child dotted venue %q; got venues %v", "cross.migrate", venues)
	}
}

// TestFlattenBundleVenues_GroupDirectStepRejected verifies a direct step under a
// pure group bundle (no workload container) is a hard error — a group has no
// venue of its own.
func TestFlattenBundleVenues_GroupDirectStepRejected(t *testing.T) {
	uf := &spec.UnifiedFile{Bundle: map[string]spec.BundleNode{
		"grp": {
			Target: "", // group, but carries a direct step → illegal
			Plan: []spec.Step{
				{Check: "stray", Op: spec.Op{Plugin: "command", PluginInput: map[string]any{"command": "true"}}},
			},
		},
	}}
	if err := loaderkit.FlattenBundleVenues(uf); err == nil {
		t.Fatalf("expected error for a direct step under a group bundle, got nil")
	}
}
