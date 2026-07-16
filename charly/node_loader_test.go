package main

import (
	"github.com/opencharly/sdk/spec"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadUnified_NodeForm proves the loader parses a unified node-form charly.yml
// end-to-end: kit.ClassifyDoc → kit.DocShapeNode → validate-before-execute (#NodeDoc) →
// normalizeNodeInto → the projected UnifiedFile maps. Candy + box + a bundle group
// with two alongside pod members + an inline cross-member check.
func TestLoadUnified_NodeForm(t *testing.T) {
	dir := t.TempDir()
	doc := `version: "` + latestSchemaVersion.String() + `"
redis:
  candy:
    version: "2026.150.0000"
    description: in-memory store
    status: working
    plan:
      - check: the binary exists
        file: /usr/bin/redis-server
coder:
  candy:
    base: fedora
    candy: [redis]
shop:
  group: {}
  web:
    pod:
      image: coder
      plan:
        - check: web reaches the cache
          command: "redis-cli -h ${HOST:cache} ping"
  cache:
    pod:
      image: coder
`
	if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified node-form: %v", err)
	}
	if redis, ok := decodeInlineCandy(uf.Candy["redis"]); !ok {
		t.Errorf("candy redis not loaded; candies=%v", mapKeys(uf.Candy))
	} else if redis.Version != "2026.150.0000" {
		t.Errorf("candy redis version = %q", redis.Version)
	}
	if coder, ok := uf.BoxConfig("coder"); !ok {
		t.Errorf("box coder not loaded; boxes=%v", boxKeys(uf.Box))
	} else if coder.Base != "fedora" {
		t.Errorf("box coder base = %q", coder.Base)
	}
	shop, ok := uf.Bundle["shop"]
	if !ok {
		t.Fatalf("bundle shop not loaded; deploys=%v", deployKeys(uf.Bundle))
	}
	if len(shop.Members) != 2 || shop.Members["web"] == nil || shop.Members["cache"] == nil {
		t.Fatalf("shop members wrong: %v", deployKeys2(shop.Members))
	}
	if shop.Members["web"].Image != "coder" {
		t.Errorf("web member box=%q, want coder", shop.Members["web"].Image)
	}
	// Post-cutover: flattenBundleVenues HOISTS the member's step into the root
	// bundle Plan, stamping venue from tree position, and CLEARS the member's own
	// Plan. So the web member's step now lives in shop.Plan with venue "web".
	if len(shop.Members["web"].Plan) != 0 {
		t.Errorf("web member Plan should be cleared after hoist, got %d", len(shop.Members["web"].Plan))
	}
	foundWebVenue := false
	for _, s := range shop.Plan {
		if s.Venue == "web" {
			foundWebVenue = true
		}
	}
	if !foundWebVenue {
		t.Errorf("expected a hoisted step with venue %q in shop.Plan", "web")
	}
}

// TestLoadUnified_RejectsLegacyShapes proves the #NodeDoc-sole-gate cutover:
// kit.ClassifyDoc hard-rejects a legacy kind-keyed document AND a legacy root-shape
// collection map (both superseded by the unified node-form), each with a
// `charly migrate` hint — the bilingual reader was deleted.
func TestLoadUnified_RejectsLegacyShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		// legacy kind-keyed single entity: `candy: {name: …}`
		{"kind-keyed candy", "candy:\n  name: redis\n  version: \"2026.150.0000\"\n"},
		// legacy root-shape collection map: `vm: {<name>: …}`
		{"root-shape vm collection", "vm:\n  myvm:\n    source: {kind: cloud_image}\n"},
		// legacy deploy-collection alias
		{"root-shape deploy collection", "deploy:\n  app:\n    box: coder\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			doc := "version: \"" + latestSchemaVersion.String() + "\"\n" + tc.body
			if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(doc), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := LoadUnified(dir)
			if err == nil {
				t.Fatalf("LoadUnified accepted a legacy %s doc; want a hard rejection", tc.name)
			}
			// A legacy shape stamped at HEAD is hard-rejected by the node-form parser
			// (a real, below-HEAD legacy config is caught by the version gate first).
			if !strings.Contains(err.Error(), "no kind discriminator") {
				t.Errorf("legacy shape must be hard-rejected, got: %v", err)
			}
			if !strings.Contains(err.Error(), "charly migrate") {
				t.Errorf("legacy-shape rejection must hint at `charly migrate`, got: %v", err)
			}
		})
	}
}

func mapKeys(m candyMap) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
func boxKeys(m boxMap) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// boxMapOf folds typed BoxConfig test literals into the generic image map — the
// test-construction analog of the loader's encodeBox (P6 map-killing). Tests author
// readable typed boxes; this marshals each opaque exactly as the loader stores them.
func boxMapOf(m map[string]spec.BoxConfig) boxMap {
	out := make(boxMap, len(m))
	for k, v := range m {
		out[k] = encodeBox(v)
	}
	return out
}
func deployKeys(m map[string]BundleNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
func deployKeys2(m map[string]*BundleNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
