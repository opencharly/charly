package deployvm

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestVmEntityForPrepare covers the ported entity-resolution logic (FINAL/K5 unit 6a, M4b —
// relocated verbatim from the deleted charly/vm_lifecycle_preresolve.go's vmEntityForAdd, which
// had no dedicated test of its own before this move). vmPrepareVenue is not itself unit-testable
// here (it drives real host reverse-channel HostBuild calls — its coverage is the check-sidecar-pod
// / check-charly-vm disposable-bed runtime gate), but this pure resolution step is.
func TestVmEntityForPrepare(t *testing.T) {
	cases := []struct {
		name    string
		node    *spec.BundleNode
		deploy  string
		want    string
		wantErr bool
	}{
		{
			name:   "node.From wins over everything else",
			node:   &spec.BundleNode{From: "cachyos-gpu"},
			deploy: "check-cachyos-gpu-vm",
			want:   "cachyos-gpu",
		},
		{
			name:   "legacy vm:<name> deploy-key prefix",
			node:   nil,
			deploy: "vm:cachyos-gpu",
			want:   "cachyos-gpu",
		},
		{
			name:   "legacy vm:<name>/<instance> form strips the instance suffix",
			node:   nil,
			deploy: "vm:cachyos-gpu/work",
			want:   "cachyos-gpu",
		},
		{
			name:   "dotted nested path falls back to the leaf",
			node:   nil,
			deploy: "check-sidecar-pod.check-sidecar-pod-ephvm",
			want:   "check-sidecar-pod-ephvm",
		},
		{
			name:   "node present but From empty falls through to the deploy-name cases",
			node:   &spec.BundleNode{Target: "vm"},
			deploy: "vm:cachyos-gpu",
			want:   "cachyos-gpu",
		},
		{
			name:    "no vm: cross-ref, no legacy prefix, no dotted leaf — errors",
			node:    nil,
			deploy:  "bare-vm-dep",
			wantErr: true,
		},
		{
			name:    "legacy vm: prefix with an empty name errors",
			node:    nil,
			deploy:  "vm:",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vmEntityForPrepare(tc.node, tc.deploy)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("vmEntityForPrepare(%q) = (%q, nil), want an error", tc.deploy, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("vmEntityForPrepare(%q) unexpected error: %v", tc.deploy, err)
			}
			if got != tc.want {
				t.Errorf("vmEntityForPrepare(%q) = %q, want %q", tc.deploy, got, tc.want)
			}
		})
	}
}
