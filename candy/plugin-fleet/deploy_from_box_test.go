package fleet

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestGenerateKubernetesKustomize_Guards exercises the three pre-dispatch validation guards that run
// BEFORE GenerateKubernetesKustomize reaches the deploy:kubernetes provider (Cone A shape 3). No
// provider needs to be registered and no executor is needed for these — each guard returns before
// touching ctx/exec.
func TestGenerateKubernetesKustomize_Guards(t *testing.T) {
	caps := &spec.BoxMetadata{}
	cluster := &spec.ResolvedKubernetes{}

	cases := []struct {
		name string
		opts KubernetesGenerateOpts
		want string
	}{
		{
			name: "missing deployment name",
			opts: KubernetesGenerateOpts{Capabilities: caps, Cluster: cluster},
			want: "deployment name is required",
		},
		{
			name: "missing capabilities",
			opts: KubernetesGenerateOpts{DeploymentName: "app", Cluster: cluster},
			want: "capabilities are required",
		},
		{
			name: "missing cluster",
			opts: KubernetesGenerateOpts{DeploymentName: "app", Capabilities: caps},
			want: "cluster profile is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GenerateKubernetesKustomize(context.TODO(), nil, tc.opts)
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if got := err.Error(); !strings.Contains(got, tc.want) {
				t.Fatalf("error = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}
