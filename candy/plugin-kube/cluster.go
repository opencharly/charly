package kube

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/opencharly/charly/candy/plugin-kube/params"
)

// cluster.go holds the cluster connection + the shared client-go helpers moved
// out of charly's core (the former charly/k8s_cmd.go). This is where the heavy
// k8s.io/client-go + k8s.io/apimachinery dependency lives — entirely out of
// charly's core go.mod.

// clusterConn carries the resolved cluster-selection inputs the plugin builds a
// rest.Config from. Unlike the in-tree k8sClusterFlags it has NO --cluster field:
// the HOST pre-resolves a `cluster: <profile>` to a concrete kubeconfig context
// (findK8sSpec → KubeconfigContext, which needs the project loader an
// out-of-process plugin cannot reach) and writes it into the plugin input's
// kube_context BEFORE marshaling the Op, so the plugin only ever sees a
// kubeconfig path + context.
type clusterConn struct {
	kubeconfig string // input kubeconfig — host path (empty → $KUBECONFIG then ~/.kube/config)
	context    string // input kube_context — kubeconfig context (empty → current-context)
}

func connFromInput(in *params.KubeInput) *clusterConn {
	return &clusterConn{kubeconfig: in.Kubeconfig, context: in.KubeContext}
}

// restConfig resolves the cluster connection to a rest.Config. Resolution:
//  1. kubeconfig path: the input's kubeconfig, else $KUBECONFIG, else ~/.kube/config.
//  2. context: the input's kube_context, else the kubeconfig current-context.
//
// The resolved context is validated against the loaded kubeconfig BEFORE handing
// off to the deferred client, so an empty or STALE context fails fast with an
// actionable message instead of a cryptic dial / TLS error at the first API call.
func (c *clusterConn) restConfig() (*rest.Config, error) {
	ctxName := c.context

	kubeconfigPath := c.kubeconfig
	if kubeconfigPath == "" {
		kubeconfigPath = os.Getenv("KUBECONFIG")
	}
	if kubeconfigPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			kubeconfigPath = filepath.Join(home, ".kube", "config")
		}
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}

	raw, err := loadingRules.Load()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	if ctxName == "" {
		ctxName = raw.CurrentContext
	}
	if ctxName == "" {
		return nil, fmt.Errorf("no kubeconfig context selected (no cluster/context resolved, and the kubeconfig has no current-context); set cluster: <name> or kube_context: <ctx>")
	}
	if _, ok := raw.Contexts[ctxName]; !ok {
		known := make([]string, 0, len(raw.Contexts))
		for name := range raw.Contexts {
			known = append(known, name)
		}
		sort.Strings(known)
		avail := "none"
		if len(known) > 0 {
			avail = strings.Join(known, ", ")
		}
		return nil, fmt.Errorf("kubeconfig context %q does not exist (known: %s); set cluster: <name> or kube_context: <ctx> to select a valid one", ctxName, avail)
	}

	overrides := &clientcmd.ConfigOverrides{CurrentContext: ctxName}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
}

func (c *clusterConn) dynamicClient() (dynamic.Interface, error) {
	cfg, err := c.restConfig()
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(cfg)
}

// Canonical GVRs used by the probe methods. Listed here so adding a new
// resource kind to probe is a one-line addition, and so the dynamic-client
// calls stay legible.
var (
	gvrNodes        = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	gvrPods         = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	gvrServices     = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
	gvrIngresses    = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	gvrIngressClass = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingressclasses"}
	gvrStorageClass = schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}
	gvrDeployments  = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	gvrDaemonSets   = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}
	gvrStatefulSets = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
)

// parseTimeout interprets a duration string like "120s" or "5m". Zero / empty /
// unparseable falls back to def. The per-method default mirrors the in-tree Kong
// `default:"…"` tags (wait-*: 120s, addons: 180s, lb-external-ip: 60s).
func parseTimeout(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// nodeReady returns true when the given Node has Ready=True among its
// conditions. Uses unstructured access since we intentionally do not import the
// typed v1.Node clientset (binary-size cost).
func nodeReady(u *unstructured.Unstructured) bool {
	conds, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, raw := range conds {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "Ready" && m["status"] == "True" {
			return true
		}
	}
	return false
}

// workloadReady checks whether a Deployment / DaemonSet / StatefulSet / Pod has
// reached its expected ready count.
func workloadReady(u *unstructured.Unstructured) bool {
	switch u.GetKind() {
	case "Deployment", "StatefulSet":
		ready, _, _ := unstructured.NestedInt64(u.Object, "status", "readyReplicas")
		want, _, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
		if want == 0 {
			return ready > 0
		}
		return ready >= want
	case "DaemonSet":
		ready, _, _ := unstructured.NestedInt64(u.Object, "status", "numberReady")
		want, _, _ := unstructured.NestedInt64(u.Object, "status", "desiredNumberScheduled")
		if want == 0 {
			return ready > 0
		}
		return ready >= want
	case "Pod":
		phase, _, _ := unstructured.NestedString(u.Object, "status", "phase")
		return phase == "Running" || phase == "Succeeded"
	}
	return false
}

// kindToGVR maps a workload kind string to its GroupVersionResource.
func kindToGVR(kind string) (schema.GroupVersionResource, bool) {
	switch strings.ToLower(kind) {
	case "deployment", "deploy":
		return gvrDeployments, true
	case "daemonset", "ds":
		return gvrDaemonSets, true
	case "statefulset", "sts":
		return gvrStatefulSets, true
	case "pod":
		return gvrPods, true
	}
	return schema.GroupVersionResource{}, false
}

// kindToPluralResource maps the Kind field of a manifest document to its
// pluralized REST resource name. Kept intentionally narrow — the apply / delete
// surface is meant for smoke-test manifests authored for the k3s verification
// flow, not production charts.
func kindToPluralResource(kind string) (string, bool) {
	switch kind {
	case "Pod":
		return "pods", true
	case "Service":
		return "services", true
	case "Deployment":
		return "deployments", true
	case "DaemonSet":
		return "daemonsets", true
	case "StatefulSet":
		return "statefulsets", true
	case "Ingress":
		return "ingresses", true
	case "ConfigMap":
		return "configmaps", true
	case "Secret":
		return "secrets", true
	case "Namespace":
		return "namespaces", true
	case "ServiceAccount":
		return "serviceaccounts", true
	case "Job":
		return "jobs", true
	case "CronJob":
		return "cronjobs", true
	case "PersistentVolumeClaim":
		return "persistentvolumeclaims", true
	}
	return "", false
}
