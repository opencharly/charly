package main

import "testing"

func TestDeriveDeploymentName(t *testing.T) {
	cases := []struct{ ref, want string }{
		{"ghcr.io/opencharly/selkies-kde-nvidia:2026.153.1026", "selkies-kde-nvidia"},
		{"localhost/charly-selkies-kde:latest", "charly-selkies-kde"},
		{"selkies-kde-nvidia", "selkies-kde-nvidia"},
		{"docker.io/library/redis:7", "redis"},
	}
	for _, c := range cases {
		if got := deriveDeploymentName(c.ref); got != c.want {
			t.Errorf("deriveDeploymentName(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

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
	project := &BundleConfig{Bundle: map[string]BundleNode{
		"cachyos-gpu": {
			Target: "vm",
			From:   "cachyos-gpu",
			Children: map[string]*BundleNode{
				"selkies-kde": {Target: "pod", Image: "selkies-kde-nvidia"},
			},
		},
	}}
	// Operator per-host overlay: per-host field set, NO nested: block.
	operator := &BundleConfig{Bundle: map[string]BundleNode{
		"cachyos-gpu": {
			Target:    "vm",
			From:      "cachyos-gpu",
			Lifecycle: "prod",
		},
	}}

	merged := MergeDeployConfigs(project, operator)
	node := merged.Bundle["cachyos-gpu"]

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
