package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"

	"github.com/opencharly/sdk/spec"
)

// methods.go is the kube method dispatcher: the 13-method probe surface moved
// from charly/k8s_cmd.go, refactored from Kong Run() methods that PRINTED to
// stdout into functions that RETURN the captured output string (so provider.go
// can feed it through the shared sdk matcher pipeline — a host-side
// matcher step does not run for an out-of-process verb). The line-oriented output
// tokens + timeouts are unchanged, so a bed authored against the in-tree verb
// passes unchanged.

// requiredModifiers mirrors the in-tree kubeMethods required-field specs (and the
// Kong `required:""` tags on the K8s*Cmd structs). The host's validate-time +
// runtime required-modifier check keyed off the former in-proc live-verb seam, which an
// external verb is not — so the check moves HERE, at dispatch, preserving the
// "missing required modifier(s): X" failure.
var requiredModifiers = map[string][]string{
	"wait-ready":     {"kube_kind", "name"},
	"lb-external-ip": {"namespace", "name"},
	"apply":          {"manifest"},
	"delete":         {"manifest"},
	"raw":            {"kube_resource"},
}

func modifierZero(op *spec.Op, name string) bool {
	switch name {
	case "kube_kind":
		return op.KubeKind == ""
	case "name":
		return op.Name == ""
	case "namespace":
		return op.Namespace == ""
	case "manifest":
		return op.Manifest == ""
	case "kube_resource":
		return op.KubeResource == ""
	}
	return false
}

func checkRequiredModifiers(method string, op *spec.Op) error {
	var missing []string
	for _, f := range requiredModifiers[method] {
		if modifierZero(op, f) {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required modifier(s): %s", strings.Join(missing, ", "))
}

// dispatch runs one kube method against the resolved cluster and returns its
// captured output. A returned error is the verb FAILING (the in-tree CLI Run()
// returning an error → exit 1); provider.go maps it through the exit_status /
// stderr matchers.
//
//nolint:gocyclo // a flat method switch over the 13-method allowlist; splitting would scatter the contract.
func dispatch(conn *clusterConn, op *spec.Op) (string, error) {
	method := string(op.Kube)
	if err := checkRequiredModifiers(method, op); err != nil {
		return "", err
	}
	switch method {
	case "nodes":
		return runNodes(conn)
	case "wait-nodes":
		return runWaitNodes(conn, op)
	case "pods":
		return runPods(conn, op)
	case "wait-ready":
		return runWaitReady(conn, op)
	case "ingress":
		return runIngress(conn, op)
	case "ingressclass":
		return runIngressClass(conn)
	case "storageclass":
		return runStorageClass(conn)
	case "service":
		return runService(conn, op)
	case "lb-external-ip":
		return runLbExternalIP(conn, op)
	case "addons":
		return runAddons(conn, op)
	case "apply":
		return runApply(conn, op)
	case "delete":
		return runDelete(conn, op)
	case "raw":
		return runRaw(conn, op)
	}
	return "", fmt.Errorf("unknown kube method %q", method)
}

// ---------------------------------------------------------------------------
// nodes
// ---------------------------------------------------------------------------

func runNodes(conn *clusterConn) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	list, err := client.Resource(gvrNodes).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("listing nodes: %w", err)
	}
	var b strings.Builder
	for _, n := range list.Items {
		state := "NotReady"
		if nodeReady(&n) {
			state = "Ready"
		}
		fmt.Fprintf(&b, "%s %s\n", n.GetName(), state)
	}
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// wait-nodes
// ---------------------------------------------------------------------------

func runWaitNodes(conn *clusterConn, op *spec.Op) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	count := op.KubeCount
	if count <= 0 {
		count = 1
	}
	deadline := time.Now().Add(parseTimeout(op.Timeout, 120*time.Second))
	for {
		list, err := client.Resource(gvrNodes).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			return "", fmt.Errorf("listing nodes: %w", err)
		}
		var b strings.Builder
		if op.Name != "" {
			for _, n := range list.Items {
				if n.GetName() == op.Name && nodeReady(&n) {
					fmt.Fprintf(&b, "%s Ready\n", op.Name)
					return b.String(), nil
				}
			}
		} else {
			ready := 0
			var names []string
			for _, n := range list.Items {
				if nodeReady(&n) {
					ready++
					names = append(names, n.GetName())
				}
			}
			if ready >= count {
				for _, n := range names {
					fmt.Fprintf(&b, "%s Ready\n", n)
				}
				return b.String(), nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for nodes Ready (want count=%d name=%q)", count, op.Name)
		}
		time.Sleep(2 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// pods
// ---------------------------------------------------------------------------

func runPods(conn *clusterConn, op *spec.Op) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	opts := metav1.ListOptions{LabelSelector: op.Label}
	var list *unstructured.UnstructuredList
	if op.Namespace == "" {
		list, err = client.Resource(gvrPods).List(context.TODO(), opts)
	} else {
		list, err = client.Resource(gvrPods).Namespace(op.Namespace).List(context.TODO(), opts)
	}
	if err != nil {
		return "", fmt.Errorf("listing pods: %w", err)
	}
	var b strings.Builder
	for _, p := range list.Items {
		phase, _, _ := unstructured.NestedString(p.Object, "status", "phase")
		fmt.Fprintf(&b, "%s/%s %s\n", p.GetNamespace(), p.GetName(), phase)
	}
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// wait-ready
// ---------------------------------------------------------------------------

func runWaitReady(conn *clusterConn, op *spec.Op) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	gvr, ok := kindToGVR(op.KubeKind)
	if !ok {
		return "", fmt.Errorf("unsupported kind %q (want Deployment, DaemonSet, StatefulSet, or Pod)", op.KubeKind)
	}
	ns := op.Namespace
	if ns == "" {
		ns = "default"
	}
	deadline := time.Now().Add(parseTimeout(op.Timeout, 120*time.Second))
	for {
		u, err := client.Resource(gvr).Namespace(ns).Get(context.TODO(), op.Name, metav1.GetOptions{})
		if err == nil && workloadReady(u) {
			return fmt.Sprintf("%s/%s Ready\n", ns, op.Name), nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("getting %s/%s: %w", ns, op.Name, err)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for %s/%s/%s Ready", op.KubeKind, ns, op.Name)
		}
		time.Sleep(2 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// ingress
// ---------------------------------------------------------------------------

func runIngress(conn *clusterConn, op *spec.Op) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	var list *unstructured.UnstructuredList
	opts := metav1.ListOptions{}
	if op.Namespace == "" {
		list, err = client.Resource(gvrIngresses).List(context.TODO(), opts)
	} else {
		list, err = client.Resource(gvrIngresses).Namespace(op.Namespace).List(context.TODO(), opts)
	}
	if err != nil {
		return "", fmt.Errorf("listing ingresses: %w", err)
	}
	var b strings.Builder
	for _, ing := range list.Items {
		class, _, _ := unstructured.NestedString(ing.Object, "spec", "ingressClassName")
		hosts := ingressHosts(&ing)
		backends := ingressBackends(&ing)
		fmt.Fprintf(&b, "%s/%s class=%s hosts=%s backends=%s\n",
			ing.GetNamespace(), ing.GetName(), class,
			strings.Join(hosts, ","), strings.Join(backends, ","))
	}
	return b.String(), nil
}

func ingressHosts(u *unstructured.Unstructured) []string {
	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	var out []string
	for _, r := range rules {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if h, ok := m["host"].(string); ok && h != "" {
			out = append(out, h)
		}
	}
	return out
}

func ingressBackends(u *unstructured.Unstructured) []string {
	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	var out []string
	for _, r := range rules {
		m, _ := r.(map[string]any)
		http, _ := m["http"].(map[string]any)
		paths, _ := http["paths"].([]any)
		for _, p := range paths {
			pm, _ := p.(map[string]any)
			backend, _ := pm["backend"].(map[string]any)
			svc, _ := backend["service"].(map[string]any)
			if name, ok := svc["name"].(string); ok {
				out = append(out, name)
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// ingressclass
// ---------------------------------------------------------------------------

func runIngressClass(conn *clusterConn) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	list, err := client.Resource(gvrIngressClass).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("listing ingress classes: %w", err)
	}
	var b strings.Builder
	for _, ic := range list.Items {
		def := ic.GetAnnotations()["ingressclass.kubernetes.io/is-default-class"]
		fmt.Fprintf(&b, "%s default=%t\n", ic.GetName(), def == "true")
	}
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// storageclass
// ---------------------------------------------------------------------------

func runStorageClass(conn *clusterConn) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	list, err := client.Resource(gvrStorageClass).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("listing storage classes: %w", err)
	}
	var b strings.Builder
	for _, sc := range list.Items {
		def := sc.GetAnnotations()["storageclass.kubernetes.io/is-default-class"]
		fmt.Fprintf(&b, "%s default=%t\n", sc.GetName(), def == "true")
	}
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// service
// ---------------------------------------------------------------------------

func runService(conn *clusterConn, op *spec.Op) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	var list *unstructured.UnstructuredList
	opts := metav1.ListOptions{}
	if op.Namespace == "" {
		list, err = client.Resource(gvrServices).List(context.TODO(), opts)
	} else {
		list, err = client.Resource(gvrServices).Namespace(op.Namespace).List(context.TODO(), opts)
	}
	if err != nil {
		return "", fmt.Errorf("listing services: %w", err)
	}
	var b strings.Builder
	for _, s := range list.Items {
		svcType, _, _ := unstructured.NestedString(s.Object, "spec", "type")
		clusterIP, _, _ := unstructured.NestedString(s.Object, "spec", "clusterIP")
		externalIP := serviceExternalIP(&s)
		fmt.Fprintf(&b, "%s/%s %s %s %s\n",
			s.GetNamespace(), s.GetName(), svcType, clusterIP, externalIP)
	}
	return b.String(), nil
}

func serviceExternalIP(u *unstructured.Unstructured) string {
	ingress, _, _ := unstructured.NestedSlice(u.Object, "status", "loadBalancer", "ingress")
	for _, raw := range ingress {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if ip, ok := m["ip"].(string); ok && ip != "" {
			return ip
		}
		if host, ok := m["hostname"].(string); ok && host != "" {
			return host
		}
	}
	ips, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "externalIPs")
	if len(ips) > 0 {
		return ips[0]
	}
	return "<none>"
}

// ---------------------------------------------------------------------------
// lb-external-ip
// ---------------------------------------------------------------------------

func runLbExternalIP(conn *clusterConn, op *spec.Op) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(parseTimeout(op.Timeout, 60*time.Second))
	for {
		svc, err := client.Resource(gvrServices).Namespace(op.Namespace).Get(context.TODO(), op.Name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("getting service: %w", err)
		}
		if err == nil {
			ip := serviceExternalIP(svc)
			if ip != "<none>" {
				return ip + "\n", nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout: no external IP assigned to %s/%s", op.Namespace, op.Name)
		}
		time.Sleep(2 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// addons — roll-up of the k3s default addon stack
// ---------------------------------------------------------------------------

// k3sAddons is the fixed list of addon workloads the `addons` method waits on.
// Names match the stock k3s deployment; if the operator explicitly disables one
// via `disable:` in k3s config, this check fails fast (which is the intended
// signal — a k3s-server candy test expects them all).
var k3sAddons = []struct {
	Kind string
	Name string
}{
	{"Deployment", "traefik"},
	{"Deployment", "local-path-provisioner"},
	{"DaemonSet", "svclb-traefik"},
}

func runAddons(conn *clusterConn, op *spec.Op) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	ns := op.Namespace
	if ns == "" {
		ns = "kube-system"
	}
	deadline := time.Now().Add(parseTimeout(op.Timeout, 180*time.Second))
	var b strings.Builder
	for _, addon := range k3sAddons {
		gvr, _ := kindToGVR(addon.Kind)
		if err := waitWorkloadReady(client, gvr, ns, addon.Name, deadline); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s/%s/%s Ready\n", addon.Kind, ns, addon.Name)
	}
	return b.String(), nil
}

func waitWorkloadReady(client dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, deadline time.Time) error {
	for {
		u, err := client.Resource(gvr).Namespace(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err == nil && workloadReady(u) {
			return nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("get %s/%s: %w", namespace, name, err)
		}
		// For svclb-traefik, fall back to prefix match on the DaemonSet list.
		if err != nil && apierrors.IsNotFound(err) && strings.HasPrefix(name, "svclb-") {
			list, lerr := client.Resource(gvr).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
			if lerr == nil {
				for _, it := range list.Items {
					if strings.HasPrefix(it.GetName(), name) && workloadReady(&it) {
						return nil
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s/%s Ready (last err: %w)", namespace, name, err)
		}
		time.Sleep(3 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// apply
// ---------------------------------------------------------------------------

func runApply(conn *clusterConn, op *spec.Op) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	docs, err := readYamlDocs(op.Manifest)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, u := range docs {
		ns := u.GetNamespace()
		if ns == "" && op.Namespace != "" {
			u.SetNamespace(op.Namespace)
			ns = op.Namespace
		}
		gvr, err := gvrForObject(u)
		if err != nil {
			return "", err
		}
		var iface dynamic.ResourceInterface = client.Resource(gvr)
		if ns != "" {
			iface = client.Resource(gvr).Namespace(ns)
		}
		_, err = iface.Create(context.TODO(), u, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			existing, gerr := iface.Get(context.TODO(), u.GetName(), metav1.GetOptions{})
			if gerr != nil {
				return "", fmt.Errorf("get existing %s/%s: %w", ns, u.GetName(), gerr)
			}
			u.SetResourceVersion(existing.GetResourceVersion())
			_, err = iface.Update(context.TODO(), u, metav1.UpdateOptions{})
		}
		if err != nil {
			return "", fmt.Errorf("apply %s/%s: %w", ns, u.GetName(), err)
		}
		fmt.Fprintf(&b, "applied %s %s/%s\n", u.GetKind(), ns, u.GetName())
	}
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// delete
// ---------------------------------------------------------------------------

func runDelete(conn *clusterConn, op *spec.Op) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	docs, err := readYamlDocs(op.Manifest)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, u := range docs {
		ns := u.GetNamespace()
		if ns == "" && op.Namespace != "" {
			ns = op.Namespace
		}
		gvr, err := gvrForObject(u)
		if err != nil {
			return "", err
		}
		var iface dynamic.ResourceInterface = client.Resource(gvr)
		if ns != "" {
			iface = client.Resource(gvr).Namespace(ns)
		}
		err = iface.Delete(context.TODO(), u.GetName(), metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			fmt.Fprintf(&b, "skip %s %s/%s (not found)\n", u.GetKind(), ns, u.GetName())
			continue
		}
		if err != nil {
			return "", fmt.Errorf("delete %s/%s: %w", ns, u.GetName(), err)
		}
		fmt.Fprintf(&b, "deleted %s %s/%s\n", u.GetKind(), ns, u.GetName())
	}
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// raw
// ---------------------------------------------------------------------------

func runRaw(conn *clusterConn, op *spec.Op) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	version := op.KubeVersion
	if version == "" {
		version = "v1"
	}
	gvr := schema.GroupVersionResource{Group: op.KubeGroup, Version: version, Resource: op.KubeResource}
	var iface dynamic.ResourceInterface = client.Resource(gvr)
	if op.Namespace != "" {
		iface = client.Resource(gvr).Namespace(op.Namespace)
	}
	if op.Name != "" {
		u, err := iface.Get(context.TODO(), op.Name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("get %s/%s: %w", op.KubeResource, op.Name, err)
		}
		return marshalJSON(u.Object)
	}
	list, err := iface.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list %s: %w", op.KubeResource, err)
	}
	sort.Slice(list.Items, func(i, j int) bool {
		if list.Items[i].GetNamespace() != list.Items[j].GetNamespace() {
			return list.Items[i].GetNamespace() < list.Items[j].GetNamespace()
		}
		return list.Items[i].GetName() < list.Items[j].GetName()
	})
	if op.JSON {
		// Emit the full Kubernetes List object so check authors who match on JSON
		// document text get the structured form (List `.kind` e.g. "NodeList",
		// `.items[]` each Unstructured).
		return marshalJSON(list.Object)
	}
	// Default list-mode output: one `<namespace>/<name>` per line.
	var b strings.Builder
	for _, u := range list.Items {
		if u.GetNamespace() != "" {
			fmt.Fprintf(&b, "%s/%s\n", u.GetNamespace(), u.GetName())
		} else {
			fmt.Fprintf(&b, "%s\n", u.GetName())
		}
	}
	return b.String(), nil
}

// marshalJSON renders a value as indented JSON text (the in-tree writeJSON shape).
func marshalJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ---------------------------------------------------------------------------
// YAML ingestion helpers (shared by apply/delete).
// ---------------------------------------------------------------------------

// readYamlDocs reads a YAML file that may contain multiple documents separated
// by `---` and decodes each into an Unstructured.
func readYamlDocs(path string) ([]*unstructured.Unstructured, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var out []*unstructured.Unstructured
	decoder := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(string(data)), 4096)
	for {
		raw := map[string]any{}
		if err := decoder.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decoding %s: %w", path, err)
		}
		if len(raw) == 0 {
			continue
		}
		out = append(out, &unstructured.Unstructured{Object: raw})
	}
	return out, nil
}

// gvrForObject maps an Unstructured to its GroupVersionResource via the static
// kind→GVR table (avoids a discovery RESTMapper — binary-size cost).
func gvrForObject(u *unstructured.Unstructured) (schema.GroupVersionResource, error) {
	gv, err := schema.ParseGroupVersion(u.GetAPIVersion())
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("parsing apiVersion %q: %w", u.GetAPIVersion(), err)
	}
	resource, ok := kindToPluralResource(u.GetKind())
	if !ok {
		return schema.GroupVersionResource{}, fmt.Errorf("unsupported kind %q for apply/delete (add mapping in kindToPluralResource)", u.GetKind())
	}
	return schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: resource}, nil
}
