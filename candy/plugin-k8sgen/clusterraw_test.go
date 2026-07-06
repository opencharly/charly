package k8sgen

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// TestInvoke_DecodesClusterRaw covers the k8s substrate-value de-type (Cutover K):
// the kernel ships the cluster body OPAQUELY in ClusterRaw and the plugin must decode
// it before generating. deployNamespace falls back to Cluster.DefaultNamespace, so
// without the ClusterRaw decode the overlay carries no namespace and this fails.
func TestInvoke_DecodesClusterRaw(t *testing.T) {
	clusterBody, err := json.Marshal(spec.K8s{Box: "app", DefaultNamespace: "myns"})
	if err != nil {
		t.Fatal(err)
	}
	params, err := json.Marshal(spec.K8sGenInput{
		DeploymentName: "app",
		ImageRef:       "registry/app:1",
		ClusterRaw:     clusterBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := (&provider{}).Invoke(context.Background(), &pb.InvokeRequest{Op: sdk.OpEmit, ParamsJson: params})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(string(reply.GetResultJson()), "myns") {
		t.Errorf("generated tree did not use ClusterRaw.DefaultNamespace %q: %s", "myns", reply.GetResultJson())
	}
}
