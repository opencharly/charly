package check

import (
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// venue_test.go — the venue-CLASSIFIER unit coverage (checkVmTarget / checkLocalTarget /
// resolveLeafVenue + the "." fast-path), ported from the deleted core charly/check_venue_test.go in
// the #118 check broker-envelope-out cutover: the classifier relocated here (venue.go), so its
// regression coverage — the RCA #12 leaf-under-pod cases + the pod-not-host-routed guard — lives
// here too. The plugin classifier reads the STAMPED venue trait (node.Descent.Venue) off the
// resolved-project deploy tree, so the fixtures set Descent.Venue directly (ssh=vm, shell=local,
// container=pod) rather than the core original's Target:/uf.VM() shape.

func desc(v string) *spec.DescentDescriptor { return &spec.DescentDescriptor{Venue: v} }

// newVenueTestTree covers every venue class the classifier must distinguish, off the stamped
// Descent.Venue trait every loader-stamped node carries.
func newVenueTestTree() map[string]spec.BundleNode {
	return map[string]spec.BundleNode{
		"cachyos-gpu": {Descent: desc("ssh")}, // vm entity (ssh venue): its own name IS the domain identity
		"web-pod": {Descent: desc("container"), Children: map[string]*spec.BundleNode{
			// RCA #12: a vm CHILD (ssh) nested under a non-vm (container) parent — the leaf, not
			// the root, is the vm. And a local (shell) leaf under the same pod root.
			"web-pod-vm":    {Descent: desc("ssh")},
			"web-pod-local": {Descent: desc("shell")},
		}},
		"k3s-vm": {Descent: desc("ssh"), Children: map[string]*spec.BundleNode{
			// Delegate-into-guest: the ROOT is the vm, the leaf is something nested INSIDE its
			// guest — the leaf check falls through to the root fallback, not a second vm.
			"inner-app": {Descent: desc("shell")},
		}},
		"bare-vm-dep": {Descent: desc("ssh")},
		"my-local":    {Descent: desc("shell")},
		"remote-host": {Descent: desc("shell"), Host: "user@box"},
	}
}

func TestCheckVmTarget(t *testing.T) {
	tree := newVenueTestTree()
	cases := []struct {
		name       string
		wantDomain string // the per-deploy DOMAIN IDENTITY (deploy key, dots→dashes) — P33
		wantOK     bool
	}{
		{"cachyos-gpu", "cachyos-gpu", true}, // vm (ssh venue): its own name IS the domain identity
		{"k3s-vm", "k3s-vm", true},           // vm deploy → the DEPLOY key
		{"bare-vm-dep", "bare-vm-dep", true},
		{"k3s-vm.inner", "k3s-vm", true}, // dotted root is the vm deploy, leaf unresolvable → root fallback
		// RCA #12: leaf-vm-under-pod — the pod ROOT is not a vm, but the LEAF is. domainID keys
		// off the FULL dotted path, sanitized by VmDomainIdentity ("." → "-").
		{"web-pod.web-pod-vm", "web-pod-web-pod-vm", true},
		// RCA #12 preserved precedent: root-vm-with-guest-suffix — the leaf resolves but is NOT
		// a vm (shell, nested inside k3s-vm's guest) → root fallback, keyed off the VM ROOT.
		{"k3s-vm.inner-app", "k3s-vm", true},
		{"web-pod", "", false},               // pod is not a vm
		{"web-pod.web-pod-local", "", false}, // leaf under a pod root that is itself local, not vm
		{"my-local", "", false},              // local is not a vm
		{"nonexistent", "", false},           // unknown
	}
	for _, tc := range cases {
		gotDomain, gotOK := checkVmTarget(tree, tc.name)
		if gotOK != tc.wantOK || gotDomain != tc.wantDomain {
			t.Errorf("checkVmTarget(%q) = (%q, %v), want (%q, %v)",
				tc.name, gotDomain, gotOK, tc.wantDomain, tc.wantOK)
		}
	}
}

func TestCheckVmTargetEmptyTree(t *testing.T) {
	if vm, ok := checkVmTarget(nil, "anything"); ok || vm != "" {
		t.Errorf("checkVmTarget(nil, …) = (%q, %v), want (\"\", false)", vm, ok)
	}
}

func TestCheckLocalTarget(t *testing.T) {
	tree := newVenueTestTree()
	cases := []struct {
		name   string
		wantOK bool
		host   string
	}{
		{"my-local", true, ""},            // shell venue (host:local default)
		{"remote-host", true, "user@box"}, // shell venue carrying host:<remote>
		{"my-local.child", true, ""},      // dotted root is shell, leaf unresolvable → root fallback
		// RCA #12: local-leaf-under-pod — the pod ROOT is not host-venue, but the LEAF (shell) is.
		{"web-pod.web-pod-local", true, ""},
		{"web-pod.web-pod-vm", false, ""}, // leaf under a pod root that is itself a vm, not local
		// A pod (container venue) is NEVER host-routed — its check venue is the running container
		// (published ports), not the host. Regression guard from commit 7a38cc3a: masked while pod
		// beds used fixed H:C==9222:9222 ports; surfaced with auto-allocated host ports.
		{"web-pod", false, ""},
		{"cachyos-gpu", false, ""}, // vm (ssh) is not a local deploy
		{"k3s-vm", false, ""},      // vm is not local
	}
	for _, tc := range cases {
		node, gotOK := checkLocalTarget(tree, tc.name)
		if gotOK != tc.wantOK {
			t.Errorf("checkLocalTarget(%q) ok = %v, want %v", tc.name, gotOK, tc.wantOK)
			continue
		}
		if gotOK && node.Host != tc.host {
			t.Errorf("checkLocalTarget(%q) node.Host = %q, want %q", tc.name, node.Host, tc.host)
		}
	}
}

func TestCheckLocalTargetEmptyTree(t *testing.T) {
	if _, ok := checkLocalTarget(nil, "anything"); ok {
		t.Errorf("checkLocalTarget(nil, …) ok = true, want false")
	}
}

// TestResolveCheckVenueLocalDot verifies the "." fast-path returns a host venue without touching
// the resolved-project envelope (the in-guest delegation target) — so it needs no executor.
func TestResolveCheckVenueLocalDot(t *testing.T) {
	v, err := resolveCheckVenue(nil, nil, "", ".", "")
	if err != nil {
		t.Fatalf("resolveCheckVenue(\".\") error: %v", err)
	}
	if v.Kind != "host" {
		t.Errorf("resolveCheckVenue(\".\").Kind = %q, want host", v.Kind)
	}
	if _, ok := v.Exec.(kit.ShellExecutor); !ok {
		t.Errorf("resolveCheckVenue(\".\").Exec = %T, want ShellExecutor", v.Exec)
	}
	if v.IsContainer() {
		t.Errorf("resolveCheckVenue(\".\").IsContainer() = true, want false")
	}
}
