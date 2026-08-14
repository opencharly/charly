package kube

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOverlayHasHelmCharts exercises the helmCharts detection that switches the
// deploy substrate between `kubectl apply -k` and the `kubectl kustomize
// --enable-helm` render+apply path (the kustomize helmCharts transformer is gated
// behind --enable-helm, which `kubectl apply -k` cannot pass).
func TestOverlayHasHelmCharts(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("kustomization.yaml", `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../base
`)
	got, err := overlayHasHelmCharts(dir)
	if err != nil {
		t.Fatalf("plain overlay: %v", err)
	}
	if got {
		t.Fatal("plain overlay reported helmCharts")
	}

	write("kustomization.yaml", `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../base
helmCharts:
  - name: prometheus-pushgateway
    releaseName: web-pushgateway
    namespace: web
    includeCRDs: true
`)
	got, err = overlayHasHelmCharts(dir)
	if err != nil {
		t.Fatalf("helm overlay: %v", err)
	}
	if !got {
		t.Fatal("helm overlay not detected")
	}

	// A missing kustomization.yaml is a hard error — the deploy must not silently
	// fall back to `apply -k` on a tree it cannot read.
	if _, err := overlayHasHelmCharts(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing kustomization.yaml did not error")
	}
}

// TestOverlayHelmNamespaces exercises the namespace extraction that feeds the
// `--enable-helm` apply path: the kustomize helmCharts transformer sets each
// rendered resource's namespace but does NOT create the namespace object, so the
// deploy must create the declared namespaces before applying the rendered stream
// (and delete them at teardown).
func TestOverlayHelmNamespaces(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// No helmCharts → no namespaces.
	write("kustomization.yaml", `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../base
`)
	ns, err := overlayHelmNamespaces(dir)
	if err != nil {
		t.Fatalf("plain overlay: %v", err)
	}
	if len(ns) != 0 {
		t.Fatalf("plain overlay reported namespaces: %v", ns)
	}

	// Two charts sharing one namespace → deduplicated to a single entry.
	write("kustomization.yaml", `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../base
helmCharts:
  - name: prometheus-pushgateway
    releaseName: web-pushgateway
    namespace: web
    includeCRDs: true
  - name: other-chart
    releaseName: other
    namespace: web
`)
	ns, err = overlayHelmNamespaces(dir)
	if err != nil {
		t.Fatalf("helm overlay: %v", err)
	}
	if len(ns) != 1 || ns[0] != "web" {
		t.Fatalf("helm overlay namespaces = %v, want [web] (deduplicated)", ns)
	}

	// A chart without a namespace contributes nothing.
	write("kustomization.yaml", `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../base
helmCharts:
  - name: prometheus-pushgateway
    releaseName: web-pushgateway
`)
	ns, err = overlayHelmNamespaces(dir)
	if err != nil {
		t.Fatalf("namespace-less helm overlay: %v", err)
	}
	if len(ns) != 0 {
		t.Fatalf("namespace-less helm overlay reported namespaces: %v", ns)
	}
}
