package bundle

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestResolveVmEntity is the regression guard for the bed-deploy reach bug
// (moved here from charly/synthetic_vm_image_test.go, W4 pure-helpers
// relocation): a kind:check bed (and any deploy.yml target:vm entry) names
// its VM via the node's `vm:` cross-ref, NOT a "vm:"-prefixed deploy name.
// Before the fix the candy compiler only recognized the "vm:" prefix, so a
// bed fell through to syntheticHostBox (host distro → pac) and the deploy
// ran `pacman` on a debian/fedora guest. resolveVmEntity must surface
// node.From so syntheticVmBox is reached.
func TestResolveVmEntity(t *testing.T) {
	cases := []struct {
		name       string
		deployName string
		node       *spec.BundleNode
		want       string
	}{
		{"bed via node.vm (the bug)", "check-fedora-vm", &spec.BundleNode{From: "fedora-vm"}, "fedora-vm"},
		{"deploy.yml target:vm via node.vm", "my-guest", &spec.BundleNode{Target: "vm", From: "arch"}, "arch"},
		{"cli vm: prefix, no node", "vm:arch", nil, "arch"},
		{"node.vm wins over prefix", "vm:ignored", &spec.BundleNode{From: "real-vm"}, "real-vm"},
		{"non-vm deploy -> empty", "my-pod", &spec.BundleNode{}, ""},
		{"nil node, non-prefixed -> empty", "some-pod", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVmEntity(tc.deployName, tc.node); got != tc.want {
				t.Errorf("resolveVmEntity(%q, %+v) = %q, want %q", tc.deployName, tc.node, got, tc.want)
			}
		})
	}
}

// TestResolveNodeOverlays_PropagatesExplicitTagToNodeVersion proves that an
// explicit --tag (c.Tag) is written onto the in-memory node.Version when the
// node carries no authored version (moved here from
// charly/bed_run_image_tag_test.go, W4 pure-helpers relocation). This is the
// vehicle by which the bed-scoped tag reaches the external-substrate
// preresolvers (k8s) + the pod overlay build (both read node.Version). Would
// FAIL without the #75 `else if tag != "" { node.Version = tag }`
// propagation in resolveNodeOverlays.
func TestResolveNodeOverlays_PropagatesExplicitTagToNodeVersion(t *testing.T) {
	c := &BundleAddCmd{Tag: "check-k8s-deploy-2026.195.0600"}
	node := &spec.BundleNode{Image: "check-k8s-deploy-app"}
	_, refStr, _, tag, err := c.resolveNodeOverlays("check-k8s-deploy-workload", node)
	if err != nil {
		t.Fatalf("resolveNodeOverlays: unexpected error: %v", err)
	}
	if node.Version != "check-k8s-deploy-2026.195.0600" {
		t.Errorf("node.Version = %q, want the propagated --tag %q (k8s/overlay preresolvers read node.Version)", node.Version, "check-k8s-deploy-2026.195.0600")
	}
	if tag != "check-k8s-deploy-2026.195.0600" {
		t.Errorf("resolved tag = %q, want %q", tag, "check-k8s-deploy-2026.195.0600")
	}
	if refStr != "check-k8s-deploy-app" {
		t.Errorf("refStr = %q, want the node.Image %q", refStr, "check-k8s-deploy-app")
	}

	// An authored node.Version WINS over --tag (existing precedence, unchanged) — the
	// propagation must not clobber an operator-pinned version.
	c2 := &BundleAddCmd{Tag: "bed-tag"}
	node2 := &spec.BundleNode{Image: "img", Version: "operator-pin"}
	if _, _, _, tag2, err := c2.resolveNodeOverlays("d", node2); err != nil {
		t.Fatalf("resolveNodeOverlays (authored version): %v", err)
	} else if tag2 != "operator-pin" || node2.Version != "operator-pin" {
		t.Errorf("authored node.Version was clobbered: tag=%q node.Version=%q, want both %q", tag2, node2.Version, "operator-pin")
	}
}
