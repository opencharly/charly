package kube

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/charly/candy/plugin-kube/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// provider.go is the out-of-process provider for ALL of plugin-kube's capabilities.
// Invoke branches on the request class: a "deploy" op drives the `deploy:k8s`
// SUBSTRATE (deploy.go — `kubectl apply -k` on the host-generated Kustomize tree);
// every other op is the `kube:` check VERB. For the verb, charly's host dispatches a
// `kube:` check step through the registry (ResolveVerb("kube") → this grpcProvider →
// Provider.Invoke) with the FULL #Op marshaled as params_json and a CheckEnv snapshot
// as env; the kube-exclusive fields ride the desugared plugin input (params.KubeInput —
// the per-verb fields left core #Op in the schema-compaction cutover). The SAME
// provider also serves the k3s post-provision finalization the deploy seam needs: that
// caller (k8s_plugin.go's invokeKubePluginWithBroker) builds a synthetic op ({method:
// k3s-post-provision, artifact_key, deploy_name} in the input map) WITH a
// reverse-channel broker and reads the result's Message. Because the out-of-process
// verb path does NOT run the host-side matcher
// pipeline, this Invoke OWNS the whole verdict:
// dispatch the method, then evaluate the stdout/stderr/exit_status matchers itself
// (via the shared sdk implementation — R3), and return the wire {status,message}
// the host decodes.

// kubeEnv is the plugin-side decode of the CheckEnv the host ships as
// Operation.Env for a `kube:` check step (provider_checkenv.go) — only Mode/Box
// matter here (kube probes a cluster, not a container, so it needs no container
// resolution). The k3s-post-provision deploy seam ships no env (the plugin reads the
// artifact key / deploy name off the plugin input and uses os.UserHomeDir itself).
type kubeEnv struct {
	Box  string `json:"box"`
	Mode string `json:"mode"` // "live" | "box"
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one operation for the plugin's capabilities. The plugin serves BOTH
// the `kube:` check verb AND the `deploy:k8s` SUBSTRATE (F1), distinguished by the
// request's class: a "deploy" op runs `kubectl apply -k` against the host-generated
// Kustomize tree (deploy.go); every other op is the `kube:` verb. It decodes the
// full #Op + the env, handles the k3s-post-provision deploy seam first, skips in box
// mode (cluster probes need a reachable cluster, never a disposable `charly check
// box`), dispatches the method, and self-evaluates the matchers.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetClass() == "deploy" {
		switch req.GetOp() {
		case sdk.OpPreresolve:
			return invokeK8sPreresolve(ctx, req)
		case sdk.OpEmit:
			// K5-A item 6: the from-box source-less path's entry point into the SAME
			// generate+write+validate logic OpPreresolve below uses (materialize.go, R3).
			return invokeK8sMaterialize(ctx, req)
		}
		return invokeDeployK8s(req)
	}
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "kube: decode op: "+err.Error())
		}
	}
	var env kubeEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	// The verb's method + kube-exclusive fields ride the desugared plugin input
	// since the schema-compaction cutover (the host preresolver writes the resolved
	// kube_context into the SAME input map).
	var in params.KubeInput
	kit.DecodeInput(op.PluginInput, &in)
	method := in.Method

	// k3s-post-provision is the k3s deploy seam (S3, FINAL/K5 unit 6 — relocated
	// wholesale from charly/k3s_post.go): retrieve-path check, guest-forward kubeconfig
	// rewrite, and the kubeconfig merge. Dispatched WITH a reverse-channel broker (the
	// host's k8s_plugin.go uses InvokeWithExecutor, mirroring the deploy:k8s preresolve
	// leg) because the guest-forward rewrite needs the "deploy-entity-resolve" HostBuild
	// seam for its LoadUnified-coupled VM lookup.
	if method == "k3s-post-provision" {
		exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
		if err != nil {
			return sdk.ResultJSON("fail", "kube: k3s-post-provision: reach host reverse channel: "+err.Error())
		}
		msg, err := k3sPostProvision(ctx, exec, k3sPostProvisionParams{ArtifactKey: in.ArtifactKey, DeployName: in.DeployName})
		if err != nil {
			return sdk.ResultJSON("fail", "kube: k3s-post-provision: "+err.Error())
		}
		return sdk.ResultJSON("pass", msg)
	}

	// Cluster-probe verb: skip under `charly check box` — there is no cluster to
	// reach on a disposable `podman run --rm` (mirrors the host's RunModeBox/box-mode skip).
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("kube: %s requires a running cluster (skip under charly check box)", method))
	}

	// Resolve the `cluster: <profile>` convenience to a concrete kubeconfig context via the
	// GENERIC cc.ResolveClusterContext reverse-leg — the host reads the project's kind:k8s spec
	// this out-of-process plugin cannot reach. Replaces the former host-side kube preresolver.
	// An empty context (no matching profile) falls back to the kubeconfig current-context.
	if in.Cluster != "" && in.KubeContext == "" {
		cc, err := sdk.NewCheckContext(req.GetExecutorBrokerId(), req.GetEnvJson())
		if err != nil {
			return sdk.ResultJSON("fail", fmt.Sprintf("kube: %s: %v", method, err))
		}
		kctx, err := cc.ResolveClusterContext(ctx, in.Cluster)
		if err != nil {
			return sdk.ResultJSON("fail", fmt.Sprintf("kube: %s: %v", method, err))
		}
		in.KubeContext = kctx
	}

	conn := connFromInput(&in)
	out, runErr := dispatch(conn, &op, &in)

	// The shared exit/stdout/stderr verdict pipeline (R3). kube produces no artifact.
	return sdk.VerbVerdict("kube", method, out, runErr, &op, false)
}
