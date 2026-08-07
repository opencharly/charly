package fleet

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// deploy_nested_pod_test.go — relocated from charly/deploy_nested_pod_test.go (#55 decoupling,
// Batch A): single test asserts deploykit.MergeDeployConfigs directly, zero charly dep.

// TestMergeDeployConfigs_VMNestedSurvivesNestedlessOverlay locks the merge
// invariant the VM target's nested-pod deploy relies on: a project VM deploy
// that declares a `nested:` target:pod child, overlaid by a per-host operator
// entry that carries its OWN per-host fields but NO `nested:` block, MUST keep
// the project's nested child after merge. This is exactly the operator
// workstation shape (~/.config/charly/deploy.yml's cachyos-gpu has
// target/vm/preemptible but no nested:) that surfaced the failure: a whole-node
// re-read of the operator deploy.yml (operator clobbering project) would drop
// nested: and silently skip plugin-deploy-vm's PostApply. The vm lifecycle hook PostApply
// consumes this merged node directly. The check-bed keys (no operator overlay)
// were never affected — which is why the bug hid behind a green pod bed. The
// end-to-end consumption proof is the live `charly check live cachyos-gpu.selkies-kde`
// R10.
func TestMergeDeployConfigs_VMNestedSurvivesNestedlessOverlay(t *testing.T) {
	project := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"cachyos-gpu": {
			Target: "vm",
			From:   "cachyos-gpu",
			Children: map[string]*spec.FleetNode{
				"selkies-kde": {Target: "pod", Image: "selkies-kde-nvidia"},
			},
		},
	}}
	// Operator per-host overlay: per-host field set, NO nested: block.
	operator := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"cachyos-gpu": {
			Target:    "vm",
			From:      "cachyos-gpu",
			Lifecycle: "prod",
		},
	}}

	merged := deploykit.MergeDeployConfigs(project, operator)
	node := merged.Fleet["cachyos-gpu"]

	// The operator overlay's non-zero field won (proves the overlay DID merge,
	// not that we merely read the project node)...
	if node.Lifecycle != "prod" {
		t.Errorf("operator Lifecycle not merged: got %q, want prod", node.Lifecycle)
	}
	// ...AND the project's nested child PASSED THROUGH the nestedless overlay.
	// A whole-node replace (the old re-read bug shape) would drop it here.
	if len(node.Children) != 1 || node.Children["selkies-kde"] == nil {
		t.Fatalf("project nested: dropped by nestedless operator overlay: %#v", node.Children)
	}
	if got := node.Children["selkies-kde"].Image; got != "selkies-kde-nvidia" {
		t.Errorf("nested child box: got %q, want selkies-kde-nvidia", got)
	}
}
