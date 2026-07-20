package substratekind

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// k8sTemplateBody builds an authored kind:k8s template RawBody carrying the
// given kubeconfig context — the shape resolveSubstrateTemplate decodes.
func k8sTemplateBody(t *testing.T, ctx string) spec.RawBody {
	t.Helper()
	body, err := json.Marshal(spec.K8s{KubeconfigContext: ctx})
	if err != nil {
		t.Fatalf("marshal k8s template body: %v", err)
	}
	return body
}

func TestK8sDeployEntries(t *testing.T) {
	deploy := map[string]*spec.Deploy{
		"openclaw": {Target: "k8s", Image: "openclaw", From: "prod-cluster"},
		"some-pod": {Target: "pod", Image: "redis"},
		"billing":  {Target: "k8s", Image: "billing", From: "stage"},
	}
	got := k8sDeployEntries(deploy)
	want := []string{"billing", "openclaw"} // sorted
	if len(got) != len(want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entries[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestK8sImageRef(t *testing.T) {
	if got := k8sImageRef("fallback-name", &spec.Deploy{}); got != "fallback-name" {
		t.Errorf("k8sImageRef with no Image = %q, want fallback to deploy name", got)
	}
	if got := k8sImageRef("fallback-name", &spec.Deploy{Image: "explicit"}); got != "explicit" {
		t.Errorf("k8sImageRef with Image set = %q, want %q", got, "explicit")
	}
}

func TestK8sSpecFor(t *testing.T) {
	templates := &spec.ProjectTemplates{
		K8s: map[string]spec.RawBody{
			"prod-cluster": k8sTemplateBody(t, "gke_prod"),
		},
	}
	node := &spec.Deploy{Target: "k8s", From: "prod-cluster"}
	got := k8sSpecFor(templates, node)
	if got == nil {
		t.Fatalf("k8sSpecFor returned nil, want a resolved spec")
	}
	if got.KubeconfigContext != "gke_prod" {
		t.Errorf("KubeconfigContext = %q, want %q", got.KubeconfigContext, "gke_prod")
	}

	// Unreferenced template → nil.
	if got := k8sSpecFor(templates, &spec.Deploy{Target: "k8s", From: "missing"}); got != nil {
		t.Errorf("k8sSpecFor(missing) = %+v, want nil", got)
	}
	if got := k8sSpecFor(nil, node); got != nil {
		t.Errorf("k8sSpecFor(nil templates) = %+v, want nil", got)
	}
}

// TestK8sTreeRoot asserts the tree-root path is <cwd>/.opencharly/k8s,
// matching charly/deploy_add_cmd_k8s.go's defaultK8sOutputDir.
func TestK8sTreeRoot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	root, err := k8sTreeRoot()
	if err != nil {
		t.Fatalf("k8sTreeRoot: %v", err)
	}
	want := filepath.Join(dir, ".opencharly", "k8s")
	if root != want {
		t.Errorf("k8sTreeRoot = %q, want %q", root, want)
	}
}

// TestCollectK8sStatus_TreePresenceAndContext exercises collectK8sStatus's
// pure per-entry logic (tree-present detection + context resolution) by
// calling its constituent pieces directly against a real on-disk tree — the
// HostBuild("resolved-project") fetch itself is proven live by the
// candy/plugin-bundle OpCompile precedent + the check-sidecar-pod R10 bed
// (charly status --json parity), not re-mocked here.
func TestCollectK8sStatus_TreePresenceAndContext(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	const name, image, tmpl, ctx = "openclaw", "openclaw", "prod-cluster", "gke_prod"
	baseDir := filepath.Join(dir, ".opencharly", "k8s", name, "base")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatalf("mkdir tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "deployment.yaml"), []byte("kind: Deployment\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	deploy := map[string]*spec.Deploy{
		name:       {Target: "k8s", Image: image, From: tmpl},
		"some-pod": {Target: "pod", Image: "redis"},
	}
	templates := &spec.ProjectTemplates{K8s: map[string]spec.RawBody{tmpl: k8sTemplateBody(t, ctx)}}

	entries := k8sDeployEntries(deploy)
	if len(entries) != 1 || entries[0] != name {
		t.Fatalf("entries = %v, want [%s] (pod deploy must be ignored)", entries, name)
	}

	treeRoot, err := k8sTreeRoot()
	if err != nil {
		t.Fatalf("k8sTreeRoot: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(treeRoot, name)); statErr != nil {
		t.Fatalf("expected tree-present for %s: %v", name, statErr)
	}

	node := deploy[name]
	if ks := k8sSpecFor(templates, node); ks == nil || ks.KubeconfigContext != ctx {
		t.Fatalf("k8sSpecFor context = %+v, want %q", ks, ctx)
	}
}
