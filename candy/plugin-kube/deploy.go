package kube

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// deploy.go — the `deploy:kubernetes` SUBSTRATE provider (F1). candy/plugin-kube serves
// BOTH the `kube:` check verb AND the `target: kubernetes` deploy substrate, so ALL
// Kubernetes cluster interaction — the client-go probe surface, the kubeconfig
// merge, and now the deploy `kubectl apply -k` — lives in this ONE plugin (R3, no
// duplicate kube path).
//
// The Kustomize GENERATOR moved into the compiled-in candy/plugin-k8sgen
// (verb:k8sgen, C8/M13). The write+egress-validate sequence that used to be a thin
// in-core shim (charly.s GenerateKubernetesKustomize, reached over the former
// the former HostBuild seam) is now DONE HERE (materialize.go, K5-A
// item 6): verb:k8sgen/verb:egress are reached peer-to-peer via InvokeProvider, and
// this plugin — a same-host subprocess with direct disk access — does its own
// MkdirAll/WriteFile. THIS plugin's own kubernetes deploy preresolver (preresolve.go,
// F6/FINAL-K5-unit-6a — dispatched directly by candy/plugin-fleet's
// preresolveSubstrate via sdk.Executor.InvokeProvider(OpPreresolve), S3b — the
// core-side deploy_preresolve.go:wireDeployPreresolver registry it used to route
// through is dissolved) GENERATES the egress-validated
// tree and ships its overlay path in DeployVenue.Substrate (spec.KubernetesDeployVenue);
// this provider does the LIVE cluster I/O it owns:
//
//   - `kubectl apply -k <overlay>` against the operator's kubeconfig (merged by
//     K3sPostProvision for a k3s cluster) — the apply IS the deploy;
//   - return the teardown op the host records in the ledger and replays at
//     `charly fleet del` (`kubectl delete -k` + remove the generated tree) —
//     record-and-replay, the external-deploy lifecycle.
//
// The plugin runs as a HOST subprocess (LocalTransport), so it reads the generated
// tree on disk and runs the host's kubectl directly — it never needs the executor
// reverse channel for kubernetes (like deploy:android).

// deployKubernetesVersion is the candy version stamped onto the ledger record (kept in
// lockstep with charly.yml + the Describe capability version).
const deployKubernetesVersion = "2026.174.1200"

// kubernetesTeardownProbeTimeout bounds the reachability probe the teardown runs before it attempts
// `kubectl delete`. Named + bounded rather than an untimed call: a wedged API server must not
// hang a `charly fleet del`, and an unreachable one must not print a connection error on
// every teardown of a vm-hosted cluster (whose API dies with the VM).
const kubernetesTeardownProbeTimeout = "5s"

// shellSingleQuote is the shared kit helper (R3 — the SAME POSIX single-quoter
// core + every other plugin alias).
var shellSingleQuote = spec.ShellQuote

// invokeDeployKubernetes handles an OpExecute Invoke for the deploy:kubernetes substrate. It
// decodes the host-preresolved venue (the generated overlay path), applies the
// Kustomize tree to the cluster, and returns the teardown op. Any apply failure is
// a hard deploy error.
func invokeDeployKubernetes(req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	venue, err := sdk.DecodeDeployVenue(req.GetEnvJson())
	if err != nil {
		return nil, fmt.Errorf("deploy:kubernetes: decode venue: %w", err)
	}
	if len(venue.Substrate) == 0 {
		return nil, fmt.Errorf("deploy:kubernetes: empty substrate payload (the host preresolver produced no KubernetesDeployVenue)")
	}
	var kv spec.KubernetesDeployVenue
	if err := json.Unmarshal(venue.Substrate, &kv); err != nil {
		return nil, fmt.Errorf("deploy:kubernetes: decode kubernetes venue: %w", err)
	}
	if kv.OverlayPath == "" {
		return nil, fmt.Errorf("deploy:kubernetes: venue carries no overlay path")
	}

	// Apply the host-generated Kustomize overlay to the cluster — the LIVE cluster
	// I/O the plugin owns (the host generated + egress-validated the tree). The
	// kube_context (from the kind:kubernetes template) targets THIS cluster explicitly via
	// `kubectl --context`, never the ambient current-context.
	ctxArgs := kubectlContextArgs(kv.KubeContext)
	if out, aerr := runKubectl(append(ctxArgs, "apply", "-k", kv.OverlayPath)...); aerr != nil {
		return nil, fmt.Errorf("deploy:kubernetes: kubectl apply -k %s: %w\n%s", kv.OverlayPath, aerr, strings.TrimSpace(out))
	}

	// Teardown, recorded in the ledger and replayed at `charly fleet del`
	// (record-and-replay). kubectl reads the operator's ~/.kube/config (no sudo) → ScopeUser.
	//
	// The cluster is routinely ALREADY GONE by teardown time: a vm-hosted k3s deploy destroys
	// the VM that serves the API. A bare `delete -k … || true` swallows the exit code but
	// still prints kubectl's `dial tcp …: connect: connection refused` to the log on every
	// single teardown. An expected, swallowed error trains readers to skim past real ones, so
	// probe reachability first and skip the delete when the cluster is gone — the same
	// idempotent-destroy shape as the vm plugin's `already_gone`. --request-timeout bounds the
	// probe so a wedged API server cannot hang teardown.
	tree := shellSingleQuote(kubernetesTreeRoot(kv))
	overlay := shellSingleQuote(kv.OverlayPath)
	ctxPrefix := kubectlContextPrefix(kv.KubeContext)
	teardown := fmt.Sprintf(
		"if kubectl %s--request-timeout=%s get --raw /readyz >/dev/null 2>&1; then "+
			"kubectl %sdelete -k %s --ignore-not-found; "+
			"else echo 'deploy:kubernetes: cluster unreachable — its workloads went with it; nothing to delete'; fi; "+
			"rm -rf %s",
		ctxPrefix, kubernetesTeardownProbeTimeout, ctxPrefix, overlay, tree)
	reverseOps := []spec.ReverseOp{sdk.PluginScriptReverseOp(spec.ScopeUser, teardown)}
	return sdk.BuildDeployReply(reverseOps, "plugin-kube", deployKubernetesVersion)
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

// kubernetesTreeRoot returns the generated tree root to remove at teardown: the host
// ships it explicitly (.opencharly/k8s/<name>), else it is derived from the
// overlay path (<root>/overlays/<inst> → <root>).
func kubernetesTreeRoot(kv spec.KubernetesDeployVenue) string {
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
