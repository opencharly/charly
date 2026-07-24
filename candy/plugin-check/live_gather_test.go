package check

import (
	"testing"

	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// TestPluginResolveVmTarget_LeafVmUnderNonVmParent ports charly/check_cmd_test.go's
// TestResolveVmTarget_LeafVmUnderNonVmParent (K1-unblock wave, arm 1) — covers RCA #13+#14
// (FINAL/K5 unit 6a): a dotted check-live name whose LEAF (not the root) is the vm — a Children
// entry nested under a non-vm parent, e.g. check-sidecar-pod.check-sidecar-pod-ephvm. Before RCA
// #13's fix, resolveVmTarget checked only the root's venue (same defect class as RCA #12's
// checkVmTarget/checkLocalTarget), so vmName never resolved past the raw dotted string. Before
// RCA #14's fix, nestedLeaf stayed nil for this shape, so loadVmCheckPlans fell through to
// deploykit.FindVmDeployNode's AMBIGUOUS top-level From-scan, which could nondeterministically
// pull in an UNRELATED same-base top-level vm's hoisted plan (Go map iteration order, proven
// live). VmSpec resolution is a SEPARATE step now (pluginResolveVmSpec, off the envelope's
// rp.Templates.VM) — this test exercises ONLY the vmName/domainID/nestedLeaf tree-walk these
// fixes target, mirroring the core original's own scoping.
func TestPluginResolveVmTarget_LeafVmUnderNonVmParent(t *testing.T) {
	// A loader-stamped node ALWAYS carries a non-nil .Descent (venue.go's header comment) — the
	// plugin's nodeTraits reads ONLY .Descent.Venue, never a Target-derived fallback (that
	// synthetic-node fallback is core-only, for un-stamped nodes built outside the loader; this
	// package never builds one). So a hand-built test tree must stamp .Descent itself, mirroring
	// what the host's stampBundleDescents pass would produce for the given Target.
	sshDescent := &spec.DescentDescriptor{Venue: "ssh"}
	tree := map[string]spec.BundleNode{
		"web-pod": {Target: "pod", Descent: &spec.DescentDescriptor{Venue: "container"}, Children: map[string]*spec.BundleNode{
			"web-pod-vm": {Target: "vm", From: "eval-vm", Descent: sshDescent},
		}},
		"k3s-vm": {Target: "vm", From: "k3s-vm-entity", Descent: sshDescent, Children: map[string]*spec.BundleNode{
			"inner-app": {Target: "local", Descent: &spec.DescentDescriptor{Venue: "host"}},
		}},
	}

	t.Run("leaf-vm-under-pod (RCA #13's new exposure)", func(t *testing.T) {
		vmName, domainID, nestedLeaf := pluginResolveVmTarget(tree, "web-pod.web-pod-vm")
		if vmName != "eval-vm" {
			t.Errorf("vmName = %q, want %q (the leaf's own entity, not the raw dotted string)", vmName, "eval-vm")
		}
		if want := vmshared.VmDomainIdentity("web-pod.web-pod-vm"); domainID != want {
			t.Errorf("domainID = %q, want %q (full dotted path, sanitized — the RCA #6-#9 canonical scheme)", domainID, want)
		}
		// RCA #14: nestedLeaf MUST be the tree-walked leaf itself here — never nil — so
		// loadVmCheckPlans sources Plan/AddCandy directly from it instead of falling through to
		// FindVmDeployNode's ambiguous scan.
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
		vmName, domainID, nestedLeaf := pluginResolveVmTarget(tree, "k3s-vm.inner-app")
		if vmName != "k3s-vm-entity" {
			t.Errorf("vmName = %q, want %q (the ROOT's entity — the leaf is not itself a vm)", vmName, "k3s-vm-entity")
		}
		if want := vmshared.VmDomainIdentity("k3s-vm"); domainID != want {
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

// TestPluginGuestNestedCheckCmd ports charly/check_cmd_test.go's TestGuestNestedCheckCmd —
// verifies the guest-side `charly check live` command pluginCheckLiveVM dispatches for a
// nested-in-VM pod (Cutover 6 delegation). The host's format/section/filter/instance selectors
// must pass through, single-quoted, so the guest produces the same report shape the host would.
// guestNestedCheckCmd itself is cmd_helpers.go's Unit A port (unchanged signature).
func TestPluginGuestNestedCheckCmd(t *testing.T) {
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

// TestPluginVmHostdevCount ports charly/vm_hostdev_test.go's TestVmHostdevCount — pins the
// nil-safety contract of the VM_HOSTDEV_COUNT intent source: a spec with no libvirt block, no
// devices block, or an empty hostdevs list all read as 0 ("no GPU configured for this VM" →
// legit N/A), and a declared hostdevs list reports its length (the GPU check then HARD-FAILS if
// the guest can't see the device).
func TestPluginVmHostdevCount(t *testing.T) {
	cases := []struct {
		name string
		spec *vmshared.VmSpec
		want int
	}{
		{"nil spec", nil, 0},
		{"nil libvirt", &vmshared.VmSpec{}, 0},
		{"nil devices", &vmshared.VmSpec{Libvirt: &spec.LibvirtDomain{}}, 0},
		{"zero hostdevs", &vmshared.VmSpec{Libvirt: &spec.LibvirtDomain{Devices: &vmshared.LibvirtDevices{}}}, 0},
		{"two hostdevs", &vmshared.VmSpec{Libvirt: &spec.LibvirtDomain{Devices: &vmshared.LibvirtDevices{
			Hostdevs: []vmshared.LibvirtHostdev{{}, {}},
		}}}, 2},
	}
	for _, tc := range cases {
		if got := pluginVmHostdevCount(tc.spec); got != tc.want {
			t.Errorf("%s: pluginVmHostdevCount = %d, want %d", tc.name, got, tc.want)
		}
	}
}
