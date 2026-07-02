package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestResolveSidecarNames covers the helper that the post-2026-05-16
// commands.go sidecar sweep depends on. Pre-fix the sweep used
// HasPrefix(name, "charly-<image>-") which over-matched Pattern-A
// instance quadlets and unrelated bases sharing the prefix
// (jupyter ⇒ over-matched jupyter-pod and jupyter/concurrency-test).
// The new sweep enumerates EXACT sidecar names from the per-host
// charly.yml overlay; this test pins the enumeration contract.
func TestResolveSidecarNames(t *testing.T) {
	tests := []struct {
		name       string
		deployYAML string
		image      string
		instance   string
		want       []string
	}{
		{
			name: "no entry — returns nil",
			deployYAML: `version: 2026.174.1100
other:
  pod:
    image: other
`,
			image:    "missing",
			instance: "",
			want:     nil,
		},
		{
			name: "entry without sidecars — returns nil",
			deployYAML: `version: 2026.174.1100
foo:
  pod:
    image: foo
`,
			image:    "foo",
			instance: "",
			want:     nil,
		},
		{
			name: "entry with one sidecar — single-name slice",
			deployYAML: `version: 2026.174.1100
foo:
  pod:
    image: foo
    sidecar:
      tailscale: {}
`,
			image:    "foo",
			instance: "",
			want:     []string{"tailscale"},
		},
		{
			name: "entry with multiple sidecars — sorted",
			deployYAML: `version: 2026.174.1100
foo:
  pod:
    image: foo
    sidecar:
      vault: {}
      tailscale: {}
`,
			image:    "foo",
			instance: "",
			want:     []string{"tailscale", "vault"},
		},
		{
			name: "Pattern-A instance entry with sidecar",
			deployYAML: `version: 2026.174.1100
foo/inst1:
  pod:
    image: foo
    sidecar:
      tailscale: {}
`,
			image:    "foo",
			instance: "inst1",
			want:     []string{"tailscale"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
				t.Fatalf("creating charly config dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "charly", "charly.yml"), []byte(tc.deployYAML), 0600); err != nil {
				t.Fatalf("writing charly.yml: %v", err)
			}

			got := resolveSidecarNames(tc.image, tc.instance)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveSidecarNames(%q, %q) = %v; want %v", tc.image, tc.instance, got, tc.want)
			}
		})
	}
}
