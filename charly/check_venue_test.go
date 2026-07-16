package main

import (
	"github.com/opencharly/sdk/spec"
	"testing"
)

// newVenueTestUF builds a small UnifiedFile covering every venue class the
// shared check-verb resolver must distinguish.
func newVenueTestUF() *UnifiedFile {
	return &UnifiedFile{
		VM: rawTemplateMap(map[string]*VmSpec{
			"cachyos-gpu": {}, // bare kind:vm entity
		}),
		Bundle: map[string]spec.BundleNode{
			"web-pod":     {Target: "pod"},
			"k3s-vm":      {Target: "vm", From: "k3s-vm-entity"},
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
		{"k3s-vm.inner", "k3s-vm", true},     // dotted root is the target:vm deploy → its domain identity
		{"web-pod", "", false},               // pod is not a VM
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
		{"my-local.child", true, ""},      // dotted root is target:local
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
	if _, ok := v.Exec.(ShellExecutor); !ok {
		t.Errorf("resolveCheckVenue(\".\").Exec = %T, want ShellExecutor", v.Exec)
	}
	if v.IsContainer() {
		t.Errorf("resolveCheckVenue(\".\").IsContainer() = true, want false")
	}
}
