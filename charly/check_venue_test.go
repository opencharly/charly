package main

import (
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// newVenueTestUF builds a small UnifiedFile covering every venue class the
// shared check-verb resolver must distinguish.
func newVenueTestUF() *UnifiedFile {
	return &UnifiedFile{
		VM: rawTemplateMap(map[string]*VmSpec{
			"cachyos-gpu": {}, // bare kind:vm entity
		}),
		Bundle: map[string]spec.BundleNode{
			"web-pod": {Target: "pod", Children: map[string]*spec.BundleNode{
				// RCA #12 (FINAL/K5 unit 6a): a target:vm CHILD nested under a
				// non-vm (pod) parent — check-sidecar-pod.check-sidecar-pod-ephvm's
				// exact shape. The leaf, not the root, is the vm.
				"web-pod-vm":    {Target: "vm"},
				"web-pod-local": {Target: "local"}, // same shape, local leaf under a pod root
			}},
			"k3s-vm": {Target: "vm", From: "k3s-vm-entity", Children: map[string]*spec.BundleNode{
				// The preserved delegate-into-guest shape: the ROOT is the vm, the
				// leaf is something else nested INSIDE that vm's guest — the leaf
				// check must fall through to the root fallback here, not treat
				// "inner-app" itself as a second vm.
				"inner-app": {Target: "local"},
			}},
			"bare-vm-dep": {Target: "vm"}, // target:vm with no explicit Vm → falls back to key
			"my-local":    {Target: "local"},
			"remote-host": {Target: "local", Host: "user@box"},
		},
	}
}

func TestCheckVmTarget(t *testing.T) {
	uf := newVenueTestUF()
	cases := []struct {
		name       string
		wantDomain string // the per-deploy DOMAIN IDENTITY (deploy key, NOT the vm: entity) — P33
		wantOK     bool
	}{
		{"cachyos-gpu", "cachyos-gpu", true}, // kind:vm entity: its own name IS the domain identity
		{"k3s-vm", "k3s-vm", true},           // target:vm deploy → the DEPLOY key (not entry.From "k3s-vm-entity")
		{"bare-vm-dep", "bare-vm-dep", true}, // target:vm, no Vm → deploy key
		{"k3s-vm.inner", "k3s-vm", true},     // dotted root is the target:vm deploy, leaf unresolvable → root fallback
		// RCA #12: leaf-vm-under-pod — the pod ROOT is not a vm, but the LEAF
		// (a Children entry) IS. domainID keys off the FULL dotted path, SANITIZED
		// by vmDomainIdentity (vmshared.VmDomainIdentity: "." → "-") — the same
		// canonical scheme every vm-state write already uses (RCA #6-#9).
		{"web-pod.web-pod-vm", "web-pod-web-pod-vm", true},
		// RCA #12 preserved precedent: root-vm-with-guest-suffix. The leaf
		// ("inner-app") resolves but is NOT itself a vm (target:local, nested
		// INSIDE k3s-vm's guest) → falls through to the root fallback, domain
		// keyed off the VM ROOT, not the leaf — the check-arch-vm.arch-host shape.
		{"k3s-vm.inner-app", "k3s-vm", true},
		{"web-pod", "", false},               // pod is not a VM
		{"web-pod.web-pod-local", "", false}, // leaf under a pod root that is itself local, not vm
		{"my-local", "", false},              // local is not a VM
		{"nonexistent", "", false},           // unknown
	}
	for _, tc := range cases {
		gotDomain, gotOK := checkVmTarget(uf, tc.name)
		if gotOK != tc.wantOK || gotDomain != tc.wantDomain {
			t.Errorf("checkVmTarget(%q) = (%q, %v), want (%q, %v)",
				tc.name, gotDomain, gotOK, tc.wantDomain, tc.wantOK)
		}
	}
}

func TestCheckVmTargetNilUF(t *testing.T) {
	if vm, ok := checkVmTarget(nil, "anything"); ok || vm != "" {
		t.Errorf("checkVmTarget(nil, …) = (%q, %v), want (\"\", false)", vm, ok)
	}
}

func TestCheckLocalTarget(t *testing.T) {
	uf := newVenueTestUF()
	cases := []struct {
		name   string
		wantOK bool
		host   string
	}{
		{"my-local", true, ""},            // host:local (default shell)
		{"remote-host", true, "user@box"}, // host:<remote> (ssh)
		{"my-local.child", true, ""},      // dotted root is target:local, leaf unresolvable → root fallback
		// RCA #12: local-leaf-under-pod — the pod ROOT is not host-venue, but the
		// LEAF (a Children entry) IS (target:local). Same defect class as
		// checkVmTarget's leaf-vm-under-pod case, same shared resolveLeafVenue fix.
		{"web-pod.web-pod-local", true, ""},
		{"web-pod.web-pod-vm", false, ""}, // leaf under a pod root that is itself a vm, not local
		{"web-pod", false, ""},            // pod is not local
		{"cachyos-gpu", false, ""},        // vm entity is not a local deploy
		{"k3s-vm", false, ""},             // target:vm is not local
	}
	for _, tc := range cases {
		node, gotOK := checkLocalTarget(uf, tc.name)
		if gotOK != tc.wantOK {
			t.Errorf("checkLocalTarget(%q) ok = %v, want %v", tc.name, gotOK, tc.wantOK)
			continue
		}
		if gotOK && node.Host != tc.host {
			t.Errorf("checkLocalTarget(%q) node.Host = %q, want %q", tc.name, node.Host, tc.host)
		}
	}
}

func TestCheckLocalTargetNilUF(t *testing.T) {
	if _, ok := checkLocalTarget(nil, "anything"); ok {
		t.Errorf("checkLocalTarget(nil, …) ok = true, want false")
	}
}

// TestCheckLocalTarget_PodNotHostRoutedWhenExternal proves a `pod` deploy is NEVER
// host-routed for check-verb venue resolution even once pod is a RECOGNIZED external
// deploy substrate at runtime (the unit test above doesn't register it, so it can't
// catch this). Regression from commit 7a38cc3a: isExternalDeploySubstrate("pod")
// became true when pod externalized, so checkLocalTarget classified a pod as a HOST
// venue → resolveCheckVenue returned Kind=host → resolveCheckEndpoint returned the raw
// container port (e.g. 127.0.0.1:9222), so cdp/vnc/spice dialed the container port on
// host loopback instead of the published host port. Masked while pod beds used fixed
// H:C==9222:9222 ports; surfaced with auto-allocated host ports. Without the
// `entry.Target != "pod"` guard in checkLocalTarget this FAILS.
func TestCheckLocalTarget_PodNotHostRoutedWhenExternal(t *testing.T) {
	registerDeclaredDeploySubstrate("pod")
	t.Cleanup(func() {
		declaredDeployMu.Lock()
		delete(declaredDeploySubstrate, "pod")
		declaredDeployMu.Unlock()
	})
	if !isExternalDeploySubstrate("pod") {
		t.Fatal("setup: pod should be a recognized external substrate after registration")
	}
	if _, ok := checkLocalTarget(newVenueTestUF(), "web-pod"); ok {
		t.Fatal("checkLocalTarget(web-pod) = true; a pod has a CONTAINER venue (published ports) and must NOT be host-routed")
	}
}

// TestParsePublishedPort (the shared host "ip:port" normalizer behind
// containerPublishedAddr) relocated to sdk/kit/ports_test.go — P12a follow-up
// (parsePublishedPort itself moved to kit.ParsePublishedPort).

// TestResolveCheckVenueLocalDot verifies the "." fast-path returns a host venue
// without touching the project config (the in-guest delegation target).
func TestResolveCheckVenueLocalDot(t *testing.T) {
	v, err := resolveCheckVenue(".", "")
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
