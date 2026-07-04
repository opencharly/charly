package kube

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// deploy.go — the `deploy:k8s` SUBSTRATE provider (F1). candy/plugin-kube serves
// BOTH the `kube:` check verb AND the `target: k8s` deploy substrate, so ALL
// Kubernetes cluster interaction — the client-go probe surface, the kubeconfig
// merge, and now the deploy `kubectl apply -k` — lives in this ONE plugin (R3, no
// duplicate kube path).
//
// The Kustomize GENERATOR moved into the compiled-in candy/plugin-k8sgen
// (verb:k8sgen, C8/M13); charly's in-core GenerateK8sKustomize is a thin shim that
// lifts the image Capabilities to ports/uid/gid, Invokes the generator's OpEmit,
// then applies the host-side egress gate + disk I/O. So the host's k8s deploy
// preresolver (charly/k8s_deploy_preresolve.go) GENERATES the egress-validated tree
// and ships its overlay path in DeployVenue.Substrate (spec.K8sDeployVenue); this
// provider does the LIVE cluster I/O it owns:
//
//   - `kubectl apply -k <overlay>` against the operator's kubeconfig (merged by
//     K3sPostProvision for a k3s cluster) — the apply IS the deploy;
//   - return the teardown op the host records in the ledger and replays at
//     `charly bundle del` (`kubectl delete -k` + remove the generated tree) —
//     record-and-replay, the external-deploy lifecycle.
//
// The plugin runs as a HOST subprocess (LocalTransport), so it reads the generated
// tree on disk and runs the host's kubectl directly — it never needs the executor
// reverse channel for k8s (like deploy:android).

// deployK8sVersion is the candy version stamped onto the ledger record (kept in
// lockstep with charly.yml + the Describe capability version).
const deployK8sVersion = "2026.174.1200"

// shellSingleQuote is the shared kit helper (R3 — the SAME POSIX single-quoter
// core + every other plugin alias).
var shellSingleQuote = kit.ShellQuote

// invokeDeployK8s handles an OpExecute Invoke for the deploy:k8s substrate. It
// decodes the host-preresolved venue (the generated overlay path), applies the
// Kustomize tree to the cluster, and returns the teardown op. Any apply failure is
// a hard deploy error.
func invokeDeployK8s(req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	venue, err := sdk.DecodeDeployVenue(req.GetEnvJson())
	if err != nil {
		return nil, fmt.Errorf("deploy:k8s: decode venue: %w", err)
	}
	if len(venue.Substrate) == 0 {
		return nil, fmt.Errorf("deploy:k8s: empty substrate payload (the host preresolver produced no K8sDeployVenue)")
	}
	var kv spec.K8sDeployVenue
	if err := json.Unmarshal(venue.Substrate, &kv); err != nil {
		return nil, fmt.Errorf("deploy:k8s: decode k8s venue: %w", err)
	}
	if kv.OverlayPath == "" {
		return nil, fmt.Errorf("deploy:k8s: venue carries no overlay path")
	}

	// Apply the host-generated Kustomize overlay to the cluster — the LIVE cluster
	// I/O the plugin owns (the host generated + egress-validated the tree). The
	// kube_context (from the kind:k8s template) targets THIS cluster explicitly via
	// `kubectl --context`, never the ambient current-context.
	ctxArgs := kubectlContextArgs(kv.KubeContext)
	if out, aerr := runKubectl(append(ctxArgs, "apply", "-k", kv.OverlayPath)...); aerr != nil {
		return nil, fmt.Errorf("deploy:k8s: kubectl apply -k %s: %w\n%s", kv.OverlayPath, aerr, strings.TrimSpace(out))
	}

	// Teardown: `kubectl [--context X] delete -k <overlay> || true; rm -rf <tree>`,
	// recorded in the ledger and replayed at `charly bundle del` (record-and-replay).
	// kubectl reads the operator's ~/.kube/config (no sudo) → ScopeUser. `|| true`
	// keeps it idempotent — the cluster may already be gone when the deploy is torn
	// down.
	teardown := fmt.Sprintf("kubectl %sdelete -k %s || true; rm -rf %s",
		kubectlContextPrefix(kv.KubeContext), shellSingleQuote(kv.OverlayPath), shellSingleQuote(k8sTreeRoot(kv)))
	reverseOps := []spec.ReverseOp{sdk.PluginScriptReverseOp(spec.ScopeUser, teardown)}
	return sdk.BuildDeployReply(reverseOps, "plugin-kube", deployK8sVersion)
}

// kubectlContextArgs returns the `--context <ctx>` argv prefix (empty when no
// context → kubectl uses the current-context).
func kubectlContextArgs(ctx string) []string {
	if ctx == "" {
		return nil
	}
	return []string{"--context", ctx}
}

// kubectlContextPrefix returns the shell-quoted `--context <ctx> ` prefix for the
// recorded teardown script (empty when no context).
func kubectlContextPrefix(ctx string) string {
	if ctx == "" {
		return ""
	}
	return "--context " + shellSingleQuote(ctx) + " "
}

// k8sTreeRoot returns the generated tree root to remove at teardown: the host
// ships it explicitly (.opencharly/k8s/<name>), else it is derived from the
// overlay path (<root>/overlays/<inst> → <root>).
func k8sTreeRoot(kv spec.K8sDeployVenue) string {
	if kv.TreeRoot != "" {
		return kv.TreeRoot
	}
	return filepath.Dir(filepath.Dir(kv.OverlayPath))
}

// runKubectl runs the host kubectl (the plugin runs as a host subprocess, so it
// reaches the operator's kubeconfig + the cluster directly).
func runKubectl(args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
