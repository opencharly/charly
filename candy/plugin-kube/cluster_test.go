package kube

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestConfigValidatesContext covers the stale-context robustification moved
// out of charly core (the former charly/k8s_context_test.go): restConfig() must
// reject a non-existent context up front with a clear, actionable error (not a
// cryptic dial/TLS failure at first API call), fall back to the kubeconfig
// current-context when no context is given, and fail clearly when no context can
// be resolved at all.
func TestRestConfigValidatesContext(t *testing.T) {
	kubeconfig := filepath.Join(t.TempDir(), "config")
	content := `apiVersion: v1
kind: Config
current-context: valid
contexts:
- name: valid
  context:
    cluster: c1
    user: u1
clusters:
- name: c1
  cluster:
    server: https://127.0.0.1:6443
users:
- name: u1
  user:
    token: tok
`
	if err := os.WriteFile(kubeconfig, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		ctx        string
		wantErr    bool
		wantSubstr string
	}{
		{"valid explicit context", "valid", false, ""},
		{"empty falls back to current-context", "", false, ""},
		{"stale explicit context rejected early", "ghost", true, "does not exist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &clusterConn{context: tc.ctx, kubeconfig: kubeconfig}
			_, err := c.restConfig()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.wantSubstr) {
					t.Errorf("err = %v, want substring %q", err, tc.wantSubstr)
				}
				if !strings.Contains(err.Error(), "valid") {
					t.Errorf("stale-context error should list known contexts, got: %v", err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}

	// No current-context and no flag → clear "no context selected" error, not a
	// cryptic downstream failure.
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &clusterConn{kubeconfig: empty}
	if _, err := c.restConfig(); err == nil || !strings.Contains(err.Error(), "no kubeconfig context selected") {
		t.Errorf("empty kubeconfig: err = %v, want 'no kubeconfig context selected'", err)
	}
}
