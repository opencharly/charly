package main

import (
	"testing"
)

// resolved_project_namespace_test.go — K1-unblock wave 2: proves the namespace-qualified
// flattening added to spec.UnifiedFile.ProjectTemplates (and the namespaced-box resolve the deleted
// resolved_project_host.go's namespaced-box fill used to drive), and the resulting functional fix to
// findK8sSpec (k8s_config.go), which previously supported ONLY root-scoped `k8s:` entity names.

// writeNamespaceImportFixture builds a minimal 2-repo-style namespace import: the root imports
// "fedora.yml" under the "fedora" alias, which declares one resolvable box (jupyter) and one
// kind:k8s cluster profile (prod-cluster) — mirroring the real vm-k3s-vm shape in this repo's own
// charly.yml (`k8s: {box: "", kubeconfig_context: ...}`).
func writeNamespaceImportFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: `+LatestSchemaVersion().String()+`
import:
  - fedora: ./fedora.yml
`)
	writeFixture(t, root, "fedora.yml", `version: `+LatestSchemaVersion().String()+`
jupyter:
    candy:
        base: quay.io/fedora/fedora:43
        build: [rpm]
        distro: [fedora]
prod-cluster:
    k8s:
        box: ""
        kubeconfig_context: fedora-prod-ctx
`)
	return root
}

// TestProjectTemplates_NamespaceQualified proves projectTemplates now flattens a namespaced
// kind:k8s entity into the envelope under its QUALIFIED key ("fedora.prod-cluster"), alongside
// (never replacing) any root-scoped entries — the gap deploy_ref.go's memory-documented spike
// identified in the resolved-project envelope (only rp.Templates.Local/K8s/... at root scope).
func TestProjectTemplates_NamespaceQualified(t *testing.T) {
	root := writeNamespaceImportFixture(t)
	uf, ok, err := LoadUnified(root)
	if err != nil || !ok || uf == nil {
		t.Fatalf("LoadUnified(%q): ok=%v err=%v", root, ok, err)
	}
	tpl := uf.ProjectTemplates()
	if tpl == nil {
		t.Fatal("ProjectTemplates returned nil")
	}
	if _, ok := tpl.K8s["prod-cluster"]; ok {
		t.Error("prod-cluster should NOT be visible at root scope (it's namespaced under 'fedora')")
	}
	body, ok := tpl.K8s["fedora.prod-cluster"]
	if !ok {
		t.Fatalf("fedora.prod-cluster missing from the namespace-flattened Templates.K8s map: %v", tpl.K8s)
	}
	if len(body) == 0 {
		t.Error("fedora.prod-cluster template body is empty")
	}
}

// TestFillNamespacedBoxes_QualifiedView moved to
// candy/plugin-build/resolved_project_projection_test.go (#55 decoupling cone, Batch B): it drove
// the namespaced-box resolve through testBuildResolvedProject, a test-side reproduction of DELETED
// production code (charly/resolved_project_host.go's namespaced-box fill) whose production home is
// now candy/plugin-build (resolveBuildEngine → projectResolvedProjectLeg). The moved test exercises
// that PRODUCTION function directly, stubbing the "buildengine-namespaced" HostBuild leg via a fake
// executor (the same pattern candy/plugin-deploy-vm/lifecycle_test.go already uses) instead of
// reproducing charly-core-only loader internals a plugin cannot import.

// TestFindK8sSpec_NamespaceQualified is the end-to-end functional proof: findK8sSpec — previously
// a bare uf.K8s[name] lookup with NO namespace support at all — now resolves a namespace-qualified
// `--cluster fedora.prod-cluster` profile via the namespace-flattened projectTemplates map, and
// correctly reports NOT FOUND for the unqualified bare name (it is namespaced, not root-scoped).
func TestFindK8sSpec_NamespaceQualified(t *testing.T) {
	root := writeNamespaceImportFixture(t)

	got := findK8sSpec(root, "fedora.prod-cluster")
	if got == nil {
		t.Fatal("findK8sSpec(fedora.prod-cluster) = nil, want a resolved K8sSpec")
	}
	if got.KubeconfigContext != "fedora-prod-ctx" {
		t.Errorf("KubeconfigContext = %q, want fedora-prod-ctx", got.KubeconfigContext)
	}

	if got := findK8sSpec(root, "prod-cluster"); got != nil {
		t.Errorf("findK8sSpec(prod-cluster) = %+v, want nil (it is namespace-scoped, not root)", got)
	}
}
