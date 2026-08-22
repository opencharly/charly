package kube

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/shellquote"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
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
var shellSingleQuote = shellquote.ShellQuote

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

	// Apply the plugin-generated Kustomize overlay (materialize.go's materializeKustomize, on the host) to the cluster — the LIVE cluster
	// I/O the plugin owns (this plugin generated + egress-validated the tree, on the host). The
	// kube_context (from the kind:kubernetes template) targets THIS cluster explicitly via
	// `kubectl --context`, never the ambient current-context.
	ctxArgs := kubectlContextArgs(kv.KubeContext)
	hasHelm, helmNamespaces, herr := overlayHelmInfo(kv.OverlayPath)
	if herr != nil {
		return nil, fmt.Errorf("deploy:kubernetes: read overlay kustomization: %w", herr)
	}
	if out, aerr := applyOverlay(ctxArgs, kv.OverlayPath, hasHelm, helmNamespaces); aerr != nil {
		return nil, fmt.Errorf("deploy:kubernetes: apply overlay %s: %w\n%s", kv.OverlayPath, aerr, strings.TrimSpace(out))
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
	// The delete mirrors the apply: a helmCharts overlay must be rendered with
	// `kubectl kustomize --enable-helm` first (`kubectl delete -k` cannot enable the
	// transformer), then deleted from the rendered stream. The namespaces the
	// helmCharts entries declared are deleted too — the apply created them, so the
	// teardown removes them (the rendered stream itself never carries a Namespace
	// object).
	deleteCmd := fmt.Sprintf("kubectl %sdelete -k %s --ignore-not-found", ctxPrefix, overlay)
	var nsDelete strings.Builder
	if hasHelm {
		deleteCmd = fmt.Sprintf("kubectl kustomize --enable-helm %s | kubectl %sdelete -f - --ignore-not-found", overlay, ctxPrefix)
		for _, ns := range helmNamespaces {
			fmt.Fprintf(&nsDelete, "kubectl %sdelete namespace %s --ignore-not-found; ", ctxPrefix, shellSingleQuote(ns))
		}
	}
	teardown := fmt.Sprintf(
		"if kubectl %s--request-timeout=%s get --raw /readyz >/dev/null 2>&1; then "+
			"%s; %s"+
			"else echo 'deploy:kubernetes: cluster unreachable — its workloads went with it; nothing to delete'; fi; "+
			"rm -rf %s",
		ctxPrefix, kubernetesTeardownProbeTimeout, deleteCmd, nsDelete.String(), tree)
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

// runKubectlStdin runs the host kubectl with the given stdin (the `apply -f -`
// stream form).
func runKubectlStdin(args []string, stdin string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// overlayHelmInfo reports whether the generated overlay kustomization carries a
// `helmCharts:` entry and the deduplicated namespaces those entries declare.
// `kubectl apply -k` / `kubectl delete -k` cannot render helmCharts — the kustomize
// helmCharts transformer is gated behind `--enable-helm`, which only `kubectl
// kustomize` accepts — so the deploy must render the overlay with `kubectl kustomize
// --enable-helm` and apply/delete the rendered stream instead. The transformer sets
// each rendered resource's namespace but does NOT create the namespace object, so
// the deploy must create the declared namespaces before applying (and delete them at
// teardown).
func overlayHelmInfo(overlayPath string) (hasHelm bool, namespaces []string, err error) {
	raw, err := os.ReadFile(filepath.Join(overlayPath, "kustomization.yaml"))
	if err != nil {
		return false, nil, err
	}
	var k struct {
		HelmCharts []struct {
			Namespace string `yaml:"namespace"`
		} `yaml:"helmCharts"`
	}
	if err := yaml.Unmarshal(raw, &k); err != nil {
		return false, nil, err
	}
	seen := make(map[string]bool)
	for _, hc := range k.HelmCharts {
		if hc.Namespace != "" && !seen[hc.Namespace] {
			seen[hc.Namespace] = true
			namespaces = append(namespaces, hc.Namespace)
		}
	}
	return len(k.HelmCharts) > 0, namespaces, nil
}

// overlayHasHelmCharts reports whether the overlay carries a `helmCharts:` entry.
func overlayHasHelmCharts(overlayPath string) (bool, error) {
	hasHelm, _, err := overlayHelmInfo(overlayPath)
	return hasHelm, err
}

// overlayHelmNamespaces returns the deduplicated namespaces the overlay's
// helmCharts entries declare.
func overlayHelmNamespaces(overlayPath string) ([]string, error) {
	_, namespaces, err := overlayHelmInfo(overlayPath)
	return namespaces, err
}

// applyOverlay applies the generated overlay to the cluster. The plain path is
// `kubectl apply -k <overlay>`; when the overlay carries a `helmCharts:` entry the
// kustomize helmCharts transformer must be enabled, which `kubectl apply -k` cannot
// do — so render with `kubectl kustomize --enable-helm` and apply the rendered
// stream via `kubectl apply -f -`. The transformer sets each rendered resource's
// namespace but does not create the namespace object, so the declared namespaces are
// created first (idempotently, via `create --dry-run=client -o yaml | apply -f -`).
func applyOverlay(ctxArgs []string, overlayPath string, hasHelm bool, namespaces []string) (string, error) {
	if !hasHelm {
		return runKubectl(append(ctxArgs, "apply", "-k", overlayPath)...)
	}
	for _, ns := range namespaces {
		manifest, err := runKubectl(append(ctxArgs, "create", "namespace", ns, "--dry-run=client", "-o", "yaml")...)
		if err != nil {
			return manifest, err
		}
		if out, err := runKubectlStdin(append(ctxArgs, "apply", "-f", "-"), manifest); err != nil {
			return out, err
		}
	}
	rendered, err := runKubectl("kustomize", "--enable-helm", overlayPath)
	if err != nil {
		return rendered, err
	}
	return runKubectlStdin(append(ctxArgs, "apply", "-f", "-"), rendered)
}
