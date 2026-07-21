package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestResolveVmTarget_LeafVmUnderNonVmParent covers RCA #13+#14 (FINAL/K5
// unit 6a): a dotted check-live name whose LEAF (not the root) is the vm — a
// Children entry nested under a non-vm parent, e.g.
// check-sidecar-pod.check-sidecar-pod-ephvm. Before RCA #13's fix,
// resolveVmTarget checked only the root's venue (same defect class as RCA
// #12's checkVmTarget/checkLocalTarget), so vmName never resolved past the
// raw dotted string, uf.VM[vmName] found nothing, and the caller's
// resolveCloudInitSSHUser(nil) SIGSEGV'd. Before RCA #14's fix, nestedLeaf
// stayed nil for this shape, so loadVmCheckPlans fell through to
// deploykit.FindVmDeployNode's AMBIGUOUS top-level From-scan, which could
// nondeterministically pull in an UNRELATED same-base top-level vm's hoisted
// plan (Go map iteration order, proven live: bed 8's REBUILD pass hit
// check-substrate's own marker check while the first pass silently matched a
// harmless sibling). uf.VM is deliberately left nil so this test exercises
// ONLY the vmName/domainID/nestedLeaf resolution these fixes target, without
// requiring a live plugin-resolved VmSpec.
func TestResolveVmTarget_LeafVmUnderNonVmParent(t *testing.T) {
	uf := &UnifiedFile{
		Bundle: map[string]spec.BundleNode{
			"web-pod": {Target: "pod", Children: map[string]*spec.BundleNode{
				"web-pod-vm": {Target: "vm", From: "eval-vm"},
			}},
			"k3s-vm": {Target: "vm", From: "k3s-vm-entity", Children: map[string]*spec.BundleNode{
				"inner-app": {Target: "local"},
			}},
		},
	}

	t.Run("leaf-vm-under-pod (RCA #13's new exposure)", func(t *testing.T) {
		c := &CheckLiveCmd{Box: "web-pod.web-pod-vm"}
		vmName, domainID, nestedLeaf, _ := c.resolveVmTarget(uf)
		if vmName != "eval-vm" {
			t.Errorf("vmName = %q, want %q (the leaf's own entity, not the raw dotted string)", vmName, "eval-vm")
		}
		if want := vmDomainIdentity("web-pod.web-pod-vm"); domainID != want {
			t.Errorf("domainID = %q, want %q (full dotted path, sanitized — the RCA #6-#9 canonical scheme)", domainID, want)
		}
		// RCA #14: nestedLeaf MUST be the tree-walked leaf itself here — never
		// nil — so loadVmCheckPlans sources Plan/AddCandy directly from it
		// instead of falling through to FindVmDeployNode's ambiguous scan.
		if nestedLeaf == nil {
			t.Fatal("nestedLeaf = nil, want the resolved web-pod-vm leaf node (RCA #14: must bypass the ambiguous top-level From-scan)")
		}
		if nestedLeaf.Target != "vm" || nestedLeaf.From != "eval-vm" {
			t.Errorf("nestedLeaf = %+v, want the web-pod-vm leaf (Target=vm, From=eval-vm)", nestedLeaf)
		}
		if len(nestedLeaf.Plan) != 0 {
			t.Errorf("nestedLeaf.Plan = %v, want empty (this member authors no plan of its own)", nestedLeaf.Plan)
		}
	})

	t.Run("root-vm-with-guest-suffix (preserved delegate-into-guest precedent)", func(t *testing.T) {
		c := &CheckLiveCmd{Box: "k3s-vm.inner-app"}
		vmName, domainID, nestedLeaf, _ := c.resolveVmTarget(uf)
		if vmName != "k3s-vm-entity" {
			t.Errorf("vmName = %q, want %q (the ROOT's entity — the leaf is not itself a vm)", vmName, "k3s-vm-entity")
		}
		if want := vmDomainIdentity("k3s-vm"); domainID != want {
			t.Errorf("domainID = %q, want %q (the vm ROOT owns the live domain)", domainID, want)
		}
		if nestedLeaf == nil {
			t.Fatal("nestedLeaf = nil, want the resolved inner-app node (guest-nested delegation)")
		}
		if nestedLeaf.Target != "local" {
			t.Errorf("nestedLeaf.Target = %q, want %q", nestedLeaf.Target, "local")
		}
	})
}

// TestGuestNestedCheckCmd verifies the guest-side `charly check live` command that
// checkLiveVM dispatches for a nested-in-VM pod (Cutover 6 delegation). The host's
// format/section/filter/instance selectors must pass through, single-quoted, so
// the guest produces the same report shape the host would.
func TestGuestNestedCheckCmd(t *testing.T) {
	cases := []struct {
		name     string
		pod      string
		format   string
		section  string
		filter   []string
		instance string
		want     string
	}{
		{
			name:   "minimal (default text)",
			pod:    "selkies-kde",
			format: "text",
			want:   "charly check live 'selkies-kde' --format 'text'",
		},
		{
			name:   "empty format defaults to text",
			pod:    "selkies-kde",
			format: "",
			want:   "charly check live 'selkies-kde' --format 'text'",
		},
		{
			name:     "all selectors pass through",
			pod:      "p",
			format:   "json",
			section:  "deploy",
			filter:   []string{"cdp", "wl"},
			instance: "work",
			want:     "charly check live 'p' --format 'json' --section 'deploy' --filter 'cdp' --filter 'wl' -i 'work'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := guestNestedCheckCmd(tc.pod, tc.format, tc.section, tc.filter, tc.instance)
			if got != tc.want {
				t.Errorf("guestNestedCheckCmd = %q, want %q", got, tc.want)
			}
		})
	}
}
