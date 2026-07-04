// Command plugin-kube is the OUT-OF-TREE charly plugin owning ALL Kubernetes
// cluster interaction: the `kube` cluster-probe check VERB, the `deploy:k8s`
// SUBSTRATE (the `target: k8s` workload deploy, F1 — `kubectl apply -k` on the
// host-generated Kustomize tree), AND the k3s kubeconfig-merge the k3s-server /
// target:k8s deploy seam needs (a standalone Go module, its own go.mod). It exists
// to keep the heavy k8s.io/client-go + k8s.io/apimachinery stack OUT of charly's
// core go.mod: the host go-builds this binary and serves it OUT-OF-PROCESS over
// go-plugin gRPC via the charly plugin SDK, so the `kube:` verb dispatches through
// the provider registry exactly like a built-in — with the verb keeping its `kube:`
// discriminator + every modifier (#KubeMethod) on charly's core #Op (authoring
// unchanged) — and `target: k8s` resolves to this plugin's deploy:k8s provider over
// the E3b reverse channel (the host preresolves the cluster template + image
// Capabilities → the egress-validated Kustomize tree, k8s_deploy_preresolve.go).
// The goadb-analog of candy/plugin-adb: the FULL client-go/clientcmd/dynamic
// dependency + the single kubectl-apply path live HERE (R3).
package main

import (
	"context"
	"embed"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

func main() { sdk.Serve(&provider{}, &meta{}) }

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the plugin's capabilities (verb:kube AND deploy:k8s) plus its
// self-contained CUE schema over the wire via sdk.BuildCapabilities. Both keep their
// entire authoring contract on charly's core schema — the verb's #KubeMethod enum +
// modifiers on #Op, the deploy substrate's fields on #Deploy / #K8s (the `k8s:`
// substrate node + the `kubernetes:` block) — so neither carries plugin_input; the
// advertised capabilities carry an EMPTY InputDef and the served schema (kube.cue)
// exists only to satisfy the host's non-empty-schema load gate. The SDK compiles the
// schema standalone here, failing loudly before serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.174.1200",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "kube", InputDef: ""},
			{Class: "deploy", Word: "k8s", InputDef: ""},
		},
		schemaFS, "schema")
}
