package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestLoadUnified_NodeForm proves the loader parses a unified node-form charly.yml
// end-to-end: kit.ClassifyDoc → kit.DocShapeNode → validate-before-execute (#NodeDoc) →
// normalizeNodeInto → the projected spec.UnifiedFile maps. Candy + box + a deploy bed in
// the POST-MIGRATE primary-substrate spelling (the former group bed unrolled — Cutover C
// task 1): a pod primary + one deploy-level pod member with an inline member check (the
// flattenVenuesByPosition venue-hoist witness).
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
  pod:
    image: coder
  cache:
    pod:
      image: coder
      plan:
        - check: cache reaches itself
          command: "redis-cli ping"
`
	if err := os.WriteFile(filepath.Join(dir, spec.UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified node-form: %v", err)
	}
	if redis, ok := spec.DecodeInlineCandy(uf.Candy["redis"]); !ok {
		t.Errorf("candy redis not loaded; candies=%v", mapKeys(uf.Candy))
	} else if redis.Version != "2026.150.0000" {
		t.Errorf("candy redis version = %q", redis.Version)
	}
	if coder, ok := uf.BoxConfig("coder"); !ok {
		t.Errorf("box coder not loaded; boxes=%v", boxKeys(uf.Box))
	} else if coder.Base != "fedora" {
		t.Errorf("box coder base = %q", coder.Base)
	}
	shop, ok := uf.Deploy["shop"]
	if !ok {
		t.Fatalf("deploy shop not loaded; deploys=%v", deployKeys(uf.Deploy))
	}
	if len(shop.Member) != 1 {
		t.Fatalf("shop members wrong: want 1, got %d (%v)", len(shop.Member), memberNames(shop.Member))
	}
	cache := shop.MemberByName("cache")
	if cache == nil {
		t.Fatalf("shop members wrong: %v", memberNames(shop.Member))
	}
	if shop.Image != "coder" {
		t.Errorf("shop primary box=%q, want coder (the promoted first member's body)", shop.Image)
	}
	// Post-cutover: flattenVenuesByPosition HOISTS the member's step into the root
	// deploy Plan, stamping venue from tree position, and CLEARS the member's own
	// Plan. So the cache member's step now lives in shop.Plan with venue "cache".
	if len(cache.Node.Plan) != 0 {
		t.Errorf("cache member Plan should be cleared after hoist, got %d", len(cache.Node.Plan))
	}
	foundCacheVenue := false
	for _, s := range shop.Plan {
		if s.Venue == "cache" {
			foundCacheVenue = true
		}
	}
	if !foundCacheVenue {
		t.Errorf("expected a hoisted step with venue %q in shop.Plan", "cache")
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
			if err := os.WriteFile(filepath.Join(dir, spec.UnifiedFileName), []byte(doc), 0o644); err != nil {
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

func mapKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
func boxKeys(m spec.BoxMap) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// boxMapOf folds typed BoxConfig test literals into the generic image map — the
// test-construction analog of the loader's encodeBox (P6 map-killing). Tests author
// readable typed boxes; this marshals each opaque exactly as the loader stores them.
func boxMapOf(m map[string]spec.BoxConfig) spec.BoxMap {
	out := make(spec.BoxMap, len(m))
	for k, v := range m {
		out[k] = spec.EncodeBox(v)
	}
	return out
}
func deployKeys(m map[string]spec.DeployNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
func memberNames(ms []spec.Member) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}
