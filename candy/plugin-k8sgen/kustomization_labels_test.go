package k8sgen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// baseKustomizationDoc returns the emitted base/kustomization.yaml as a decoded map.
func baseKustomizationDoc(t *testing.T, in spec.K8sGenInput) map[string]any {
	t.Helper()
	reply, err := GenerateTree(in)
	if err != nil {
		t.Fatalf("GenerateTree: %v", err)
	}
	var paths []string
	for _, f := range reply.Files {
		paths = append(paths, f.RelPath)
		if !strings.HasSuffix(f.RelPath, "base/kustomization.yaml") {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(f.Doc, &doc); err != nil {
			t.Fatalf("decode %s: %v", f.RelPath, err)
		}
		return doc
	}
	t.Fatalf("no base/kustomization.yaml emitted; got %v", paths)
	return nil
}

// TestBaseKustomization_UsesLabelsNotCommonLabels pins the kustomize migration.
//
// kustomize deprecated `commonLabels` and prints, on EVERY `kubectl apply -k`:
//
//	# Warning: 'commonLabels' is deprecated. Please use 'labels' instead.
//
// The generated Kustomization is ours, so the warning is ours to clear. `labels` with
// includeSelectors:true is the exact replacement — it adds the pairs AND writes them into
// the selectors, which is what commonLabels did. Plain `labels` WITHOUT includeSelectors
// would silently stop labelling selectors, so that flag is asserted here too: dropping it
// is a real behaviour regression, not cosmetics.
//
// Reverting the generator to `commonLabels` fails this test.
func TestBaseKustomization_UsesLabelsNotCommonLabels(t *testing.T) {
	doc := baseKustomizationDoc(t, spec.K8sGenInput{
		DeploymentName: "check-workload",
		ImageRef:       "example.invalid/img:v1",
	})

	if _, bad := doc["commonLabels"]; bad {
		t.Fatalf("base/kustomization.yaml still uses the deprecated commonLabels: %v", doc)
	}
	raw, ok := doc["labels"]
	if !ok {
		t.Fatalf("base/kustomization.yaml carries no labels: field: %v", doc)
	}
	entries, ok := raw.([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("labels: must be a non-empty list, got %#v", raw)
	}
	first, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("labels[0] must be a mapping, got %#v", entries[0])
	}
	if inc, _ := first["includeSelectors"].(bool); !inc {
		t.Fatalf("labels[0].includeSelectors must be true to preserve commonLabels semantics (it writes the pairs into selectors), got %#v", first)
	}
	pairs, ok := first["pairs"].(map[string]any)
	if !ok || len(pairs) == 0 {
		t.Fatalf("labels[0].pairs must be a non-empty map, got %#v", first["pairs"])
	}
	// The default app label must survive the migration.
	if pairs["app"] != "check-workload" {
		t.Fatalf("the app label must carry the deployment name, got %#v", pairs)
	}
}
