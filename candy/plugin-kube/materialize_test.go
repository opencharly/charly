package kube

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestMaterializeKustomize_Guards exercises the three pre-dispatch validation guards —
// mirroring the host-side TestGenerateK8sKustomize_Guards (charly/k8s_generate_test.go) exactly,
// since both now gate the SAME K5-A item-6 contract from either side of the process boundary. No
// executor/broker is exercised — each guard returns before touching exec.InvokeProvider.
func TestMaterializeKustomize_Guards(t *testing.T) {
	cases := []struct {
		name string
		req  spec.K8sGenerateKustomizeRequest
		want string
	}{
		{
			name: "missing deployment name",
			req:  spec.K8sGenerateKustomizeRequest{CapsJSON: []byte("{}"), ClusterJSON: []byte("{}")},
			want: "deployment name is required",
		},
		{
			name: "missing capabilities",
			req:  spec.K8sGenerateKustomizeRequest{Name: "app", ClusterJSON: []byte("{}")},
			want: "capabilities are required",
		},
		{
			name: "missing cluster",
			req:  spec.K8sGenerateKustomizeRequest{Name: "app", CapsJSON: []byte("{}")},
			want: "cluster profile is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := materializeKustomize(context.Background(), nil, tc.req)
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if got := err.Error(); !strings.Contains(got, tc.want) {
				t.Fatalf("error = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}
