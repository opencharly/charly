package main

import (
	"strings"
	"testing"
)

// TestRewriteServerPorts covers the k3s kubeconfig server-rewrite: the retrieved
// kubeconfig's guest-local k3s port (6443) must map to the VM's host-forwarded port
// (16443) so host-side kubectl reaches the in-guest API. Without it kubectl dials
// 127.0.0.1:6443 → connection refused.
func TestRewriteServerPorts(t *testing.T) {
	in := "clusters:\n- cluster:\n    server: https://127.0.0.1:6443\n  name: default\n"
	out := rewriteServerPorts(in, map[string]string{"6443": "16443"})
	if !strings.Contains(out, "server: https://127.0.0.1:16443") {
		t.Errorf("server not rewritten to the forwarded port 16443:\n%s", out)
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
