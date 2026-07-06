package main

import (
	"reflect"
	"testing"
)

// TestBedVmDomains proves the per-domain serialization key-gathering: a bed's VM domains
// are collected from its OWN vm target AND from any group-member vm targets, deduped +
// sorted. This is what makes a direct `from: k3s-vm` bed (check-k3s-vm) and a GROUP whose
// member is `from: k3s-vm` (check-k8s-deploy's cluster member) resolve to the SAME domain
// charly-k3s-vm — so runCheckBed serializes them on one lock instead of clobbering. Without
// gathering the MEMBER domain, the group bed would take no lock and collide (the incident).
func TestBedVmDomains(t *testing.T) {
	if got := bedVmDomains(BundleNode{Target: "vm", From: "k3s-vm"}); !reflect.DeepEqual(got, []string{"charly-k3s-vm"}) {
		t.Fatalf("direct vm bed: got %v, want [charly-k3s-vm]", got)
	}
	group := BundleNode{Target: "group", Members: map[string]*BundleNode{
		"check-k8s-deploy-cluster":  {Target: "vm", From: "k3s-vm"},
		"check-k8s-deploy-workload": {Target: "k8s"},
	}}
	if got := bedVmDomains(group); !reflect.DeepEqual(got, []string{"charly-k3s-vm"}) {
		t.Fatalf("group with a vm member: got %v, want [charly-k3s-vm] (must gather the MEMBER domain)", got)
	}
	if got := bedVmDomains(BundleNode{Target: "pod"}); len(got) != 0 {
		t.Fatalf("non-vm bed: got %v, want no domains", got)
	}
	// Two distinct vm domains (a hypothetical multi-vm group) come back sorted + deduped.
	multi := BundleNode{Target: "group", Members: map[string]*BundleNode{
		"b": {Target: "vm", From: "vm-b"},
		"a": {Target: "vm", From: "vm-a"},
		"d": {Target: "vm", From: "vm-a"}, // dup of vm-a
	}}
	if got := bedVmDomains(multi); !reflect.DeepEqual(got, []string{"charly-vm-a", "charly-vm-b"}) {
		t.Fatalf("multi-vm group: got %v, want sorted deduped [charly-vm-a charly-vm-b]", got)
	}
}
