package main

import (
	"strings"
	"testing"
)

// TestRewriteServerPorts covers the k3s kubeconfig server-rewrite: the retrieved
// kubeconfig's guest-local k3s port (6443) must map to the VM's auto-allocated
// host-forwarded port (here 45000, representative) so host-side kubectl reaches the
// in-guest API. Without it kubectl dials 127.0.0.1:6443 → connection refused.
func TestRewriteServerPorts(t *testing.T) {
	in := "clusters:\n- cluster:\n    server: https://127.0.0.1:6443\n  name: default\n"
	out := rewriteServerPorts(in, map[string]string{"6443": "45000"})
	if !strings.Contains(out, "server: https://127.0.0.1:45000") {
		t.Errorf("server not rewritten to the forwarded port 45000:\n%s", out)
	}
	if strings.Contains(out, ":6443") {
		t.Errorf("guest port 6443 still present after rewrite:\n%s", out)
	}
}

// TestRewriteServerPorts_NoMappingNoChange leaves the config untouched when no forward
// maps the server's port.
func TestRewriteServerPorts_NoMappingNoChange(t *testing.T) {
	in := "    server: https://10.0.0.5:6443\n"
	if out := rewriteServerPorts(in, map[string]string{"5900": "15900"}); out != in {
		t.Errorf("unrelated forward changed the config: %q", out)
	}
}

// TestResolveDeployForwards covers deployVMForwards' pure core: an `auto:<guest>`
// entry resolves to its persisted auto-allocated host port; a fixed entry passes
// through; an auto entry with NO persisted allocation is a LOUD ERROR (post-vm-create
// the allocation must exist — a miss is a persist/read key mismatch, never a silent drop).
func TestResolveDeployForwards(t *testing.T) {
	// auto resolves to the persisted host port.
	got, err := resolveDeployForwards([]string{"auto:6443"}, map[string]int{"6443": 45000})
	if err != nil || len(got) != 1 || got[0] != "45000:6443" {
		t.Errorf("auto:6443 with alloc{6443:45000} → %v, %v; want [45000:6443], nil", got, err)
	}
	// fixed passthrough + mixed.
	got, err = resolveDeployForwards([]string{"auto:6443", "2222:22"}, map[string]int{"6443": 45000})
	if err != nil || len(got) != 2 || got[0] != "45000:6443" || got[1] != "2222:22" {
		t.Errorf("mixed → %v, %v; want [45000:6443 2222:22], nil", got, err)
	}
	// auto with NO persisted allocation → LOUD ERROR (never a silent drop / bogus literal).
	if _, err := resolveDeployForwards([]string{"auto:6443"}, nil); err == nil {
		t.Errorf("auto with no persisted allocation must ERROR (loud), got nil err")
	}
	// a fixed-only list needs no allocation → no error (passthrough).
	got, err = resolveDeployForwards([]string{"8080:80"}, nil)
	if err != nil || len(got) != 1 || got[0] != "8080:80" {
		t.Errorf("fixed-only → %v, %v; want [8080:80], nil", got, err)
	}
}
