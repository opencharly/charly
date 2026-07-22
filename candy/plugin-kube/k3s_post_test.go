package kube

import (
	"context"
	"testing"
)

// TestK3sPostProvision_NoRetrievedKubeconfig_NoOp proves the S3-relocated
// finalization's first gate: a deploy whose artifact retrieve never produced (or
// skipped) ~/.cache/charly/clusters/<artifact-key>/kubeconfig.yaml is a clean no-op
// (empty message, nil error) — it must NOT reach rewriteK3sServerToForward (which
// dials the host reverse channel via exec.HostBuild and would panic on the nil
// *sdk.Executor this test deliberately passes), proving a non-k3s-server deploy's
// artifact retrieve never mis-dispatches into this plugin's HostBuild leg. A
// regression that dropped the early os.Stat gate would fail this test with a nil
// pointer panic.
func TestK3sPostProvision_NoRetrievedKubeconfig_NoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	msg, err := k3sPostProvision(context.Background(), nil, k3sPostProvisionParams{
		ArtifactKey: "no-such-cluster",
		DeployName:  "no-such-deploy",
	})
	if err != nil {
		t.Fatalf("k3sPostProvision: want nil error for a never-retrieved kubeconfig, got %v", err)
	}
	if msg != "" {
		t.Fatalf("k3sPostProvision: want an empty (no-op) message, got %q", msg)
	}
}
