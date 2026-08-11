package substratekind

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
)

// kubernetesTemplateBody builds an authored kind:kubernetes template RawBody carrying the
// given kubeconfig context — the shape resolveSubstrateTemplate decodes.
func kubernetesTemplateBody(t *testing.T, ctx string) spec.RawBody {
	t.Helper()
	body, err := json.Marshal(spec.Kubernetes{KubeconfigContext: ctx})
	if err != nil {
		t.Fatalf("marshal kubernetes template body: %v", err)
	}
	return body
}

func TestKubernetesDeployEntries(t *testing.T) {
	deploy := map[string]*spec.Deploy{
		"openclaw": {Target: "kubernetes", Image: "openclaw", From: "prod-cluster"},
		"some-pod": {Target: "pod", Image: "redis"},
		"billing":  {Target: "kubernetes", Image: "billing", From: "stage"},
	}
	got := kubernetesDeployEntries(deploy)
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

func TestKubernetesImageRef(t *testing.T) {
	if got := kubernetesImageRef("fallback-name", &spec.Deploy{}); got != "fallback-name" {
		t.Errorf("kubernetesImageRef with no Image = %q, want fallback to deploy name", got)
	}
	if got := kubernetesImageRef("fallback-name", &spec.Deploy{Image: "explicit"}); got != "explicit" {
		t.Errorf("kubernetesImageRef with Image set = %q, want %q", got, "explicit")
	}
}

func TestKubernetesSpecFor(t *testing.T) {
	templates := &spec.ProjectTemplates{
		Kubernetes: map[string]spec.RawBody{
			"prod-cluster": kubernetesTemplateBody(t, "gke_prod"),
		},
	}
	node := &spec.Deploy{Target: "kubernetes", From: "prod-cluster"}
	got := kubernetesSpecFor(templates, node)
	if got == nil {
		t.Fatalf("kubernetesSpecFor returned nil, want a resolved spec")
	}
	if got.KubeconfigContext != "gke_prod" {
		t.Errorf("KubeconfigContext = %q, want %q", got.KubeconfigContext, "gke_prod")
	}

	// Unreferenced template → nil.
	if got := kubernetesSpecFor(templates, &spec.Deploy{Target: "kubernetes", From: "missing"}); got != nil {
		t.Errorf("kubernetesSpecFor(missing) = %+v, want nil", got)
	}
	if got := kubernetesSpecFor(nil, node); got != nil {
		t.Errorf("kubernetesSpecFor(nil templates) = %+v, want nil", got)
	}
}

// TestKubernetesTreeRoot asserts the tree-root path is <cwd>/.opencharly/k8s —
// the canonical root the Kustomize generator emits trees under.
func TestKubernetesTreeRoot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	root, err := kubernetesTreeRoot()
	if err != nil {
		t.Fatalf("kubernetesTreeRoot: %v", err)
	}
	want := filepath.Join(dir, ".opencharly", "k8s")
	if root != want {
		t.Errorf("kubernetesTreeRoot = %q, want %q", root, want)
	}
}

// TestCollectKubernetesStatus_TreePresenceAndContext exercises collectKubernetesStatus's
// pure per-entry logic (tree-present detection + context resolution) by
// calling its constituent pieces directly against a real on-disk tree — the
// InvokeProvider("build","project") fetch itself is proven live by the
// candy/plugin-fleet OpCompile precedent + the check-sidecar-pod R10 bed
// (charly status --json parity), not re-mocked here.
func TestCollectKubernetesStatus_TreePresenceAndContext(t *testing.T) {
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
		name:       {Target: "kubernetes", Image: image, From: tmpl},
		"some-pod": {Target: "pod", Image: "redis"},
	}
	templates := &spec.ProjectTemplates{Kubernetes: map[string]spec.RawBody{tmpl: kubernetesTemplateBody(t, ctx)}}

	entries := kubernetesDeployEntries(deploy)
	if len(entries) != 1 || entries[0] != name {
		t.Fatalf("entries = %v, want [%s] (pod deploy must be ignored)", entries, name)
	}

	treeRoot, err := kubernetesTreeRoot()
	if err != nil {
		t.Fatalf("kubernetesTreeRoot: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(treeRoot, name)); statErr != nil {
		t.Fatalf("expected tree-present for %s: %v", name, statErr)
	}

	node := deploy[name]
	if ks := kubernetesSpecFor(templates, node); ks == nil || ks.KubeconfigContext != ctx {
		t.Fatalf("kubernetesSpecFor context = %+v, want %q", ks, ctx)
	}
}
