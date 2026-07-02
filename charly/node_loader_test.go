package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadUnified_NodeForm proves the loader parses a unified node-form charly.yml
// end-to-end: classifyDoc → docShapeNode → validate-before-execute (#NodeDoc) →
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
  redis-step-0:
    check: the binary exists
    file: /usr/bin/redis-server
coder:
  candy:
    base: fedora
  coder-candy:
    candy: [redis]
shop:
  group: {}
  web:
    pod:
      image: coder
    web-step-0:
      check: web reaches the cache
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
	if uf.Candy["redis"] == nil {
		t.Errorf("candy redis not loaded; candies=%v", mapKeys(uf.Candy))
	} else if uf.Candy["redis"].Version != "2026.150.0000" {
		t.Errorf("candy redis version = %q", uf.Candy["redis"].Version)
	}
	if _, ok := uf.Box["coder"]; !ok {
		t.Errorf("box coder not loaded; boxes=%v", boxKeys(uf.Box))
	} else if uf.Box["coder"].Base != "fedora" {
		t.Errorf("box coder base = %q", uf.Box["coder"].Base)
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
// classifyDoc hard-rejects a legacy kind-keyed document AND a legacy root-shape
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
			if !strings.Contains(err.Error(), "no discriminator") {
				t.Errorf("legacy shape must be hard-rejected, got: %v", err)
			}
		})
	}
}

func mapKeys(m map[string]*InlineCandy) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
func boxKeys(m map[string]BoxConfig) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
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
