package main

import (
	"testing"
)

// resolved_project_namespace_test.go — K1-unblock wave 2: proves the namespace-qualified
// flattening added to spec.UnifiedFile.ProjectTemplates (and the namespaced-box resolve the deleted
// resolved_project_host.go's namespaced-box fill used to drive) — the SAME LoadUnified walk/
// materialize algorithm every kind:<word> template lookup relies on, whether reached in-proc
// (this test, and the deleted "deploy-entity-resolve" HostBuild seam that used to wrap it) or
// PLUGIN-SIDE over the reverse channel (sdk/loaderkit.LoadUnifiedViaExecutor, K-wave W3a
// A3-phase-2's Resolve{K8s,Vm,Android}EntityViaExecutor — LoadUnifiedViaExecutor's own doc
// comment: "Returns the fully-merged, validated project the SAME way the compiled-in host loader
// does"). The former "deploy-entity-resolve" seam + its
// TestHostBuildDeployEntityResolve_K8sNamespaceQualified functional-proof test are BOTH deleted
// (the seam has zero callers left); this test's own coverage of the underlying
// ProjectTemplates()/namespace-qualification claim is unaffected — it never depended on the seam.

// writeNamespaceImportFixture builds a minimal 2-repo-style namespace import: the root imports
// "fedora.yml" under the "fedora" alias, which declares one resolvable box (jupyter) and one
// kind:k8s cluster profile (prod-cluster) — mirroring the real check-k3s-vm-ctx shape in this
// repo's own charly.yml (`k8s: {box: "", kubeconfig_context: ...}`).
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

// TestHostBuildDeployEntityResolve_K8sNamespaceQualified DELETED (K-wave W3a A3-phase-2): its
// subject, hostBuildDeployEntityResolve (the "deploy-entity-resolve" HostBuild seam), is deleted —
// every kind:<word> caller self-loads the project now via sdk/loaderkit.LoadUnifiedViaExecutor,
// which drives the SAME LoadUnified walk/materialize algorithm TestProjectTemplates_NamespaceQualified
// above already proves namespace-qualifies correctly (LoadUnifiedViaExecutor's own doc comment:
// "Returns the fully-merged, validated project the SAME way the compiled-in host loader does" —
// the algorithm is placement-invariant; only the seam WIRING differs between in-proc and
// HostBuild-dispatched). The plugin-side seam-dispatch wiring itself is proven live by this
// unit's disposable-bed roster (check-k8s-deploy et al), not a narrow unit test — the established
// pattern every other sdk/loaderkit self-load consumer in this tree already follows (no existing
// unit test exercises LoadUnifiedViaExecutor's own HostBuild round trip directly either).
