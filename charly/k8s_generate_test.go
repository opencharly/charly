package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestGenerateK8sKustomize_Guards exercises the three pre-dispatch validation guards that run
// BEFORE GenerateK8sKustomize reaches the deploy:k8s provider (K5-A item 6 — the write+validate
// body moved into candy/plugin-kube/materialize.go, but these guards stayed host-side since they
// gate the request BEFORE any dispatch, exactly as the pre-move body did). No provider needs to
// be registered for these — each guard returns before touching providerRegistry.
func TestGenerateK8sKustomize_Guards(t *testing.T) {
	caps := &spec.BoxMetadata{}
	cluster := &ResolvedK8s{}

	cases := []struct {
		name string
		opts K8sGenerateOpts
		want string
	}{
		{
			name: "missing deployment name",
			opts: K8sGenerateOpts{Capabilities: caps, Cluster: cluster},
			want: "deployment name is required",
		},
		{
			name: "missing capabilities",
			opts: K8sGenerateOpts{DeploymentName: "app", Cluster: cluster},
			want: "capabilities are required",
		},
		{
			name: "missing cluster",
			opts: K8sGenerateOpts{DeploymentName: "app", Capabilities: caps},
			want: "cluster profile is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GenerateK8sKustomize(tc.opts)
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if got := err.Error(); !strings.Contains(got, tc.want) {
				t.Fatalf("error = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}
