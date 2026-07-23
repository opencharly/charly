package main

// cross_kind_name_test.go — locks in the cross-kind name reuse policy under
// the unified node-form model. The boundary moved with the node-form cutover:
//
//   - ACROSS SEPARATE discovered documents (files), the SAME identifier (e.g.
//     `redis`) MAY name a box in one file AND a candy in another — they are
//     distinct documents that register into distinct internal maps.
//   - WITHIN ONE document, every top-level node name is GLOBALLY UNIQUE — a
//     box `x` and a local/vm/candy `x` both flatten to the one top-level YAML
//     key `x: {…}`, so they COLLIDE: yaml merges the two `x:` mappings into a
//     single node carrying two entity discriminators, which the closed #NodeDoc
//     schema rejects (a node has exactly one discriminator). The loader fails.
//
// charly verbs still disambiguate cross-FILE reuse by command context:
// `charly box build redis` reaches the box document, the candy resolver reaches
// the candy directory.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCrossKindNameReuse_LoaderAcceptsAllKinds — the SAME name `redis` names a
// box in box/redis/charly.yml AND a candy in candy/redis/charly.yml. Because
// they are SEPARATE discovered documents, LoadUnified accepts the reuse and
// registers each into its own map. Within ONE document, however, a duplicate
// top-level node name collides and the loader rejects it.
func TestCrossKindNameReuse_LoaderAcceptsAllKinds(t *testing.T) {
	// --- Cross-FILE reuse: accepted. ---
	dir := t.TempDir()
	must := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(dir, "charly.yml"), `version: 2026.204.1223
defaults:
  registry: ghcr.io/example
discover:
  - path: box
    recursive: true
  - path: candy
    recursive: true
`)
	// A box named `redis` in its own discovered document (node-form).
	must(filepath.Join(dir, "box", "redis", "charly.yml"), `redis:
  candy:
    base: fedora
`)
	// A candy ALSO named `redis` in a SEPARATE discovered document. Compact
	// node-form: the FULL body — package collection and plan steps included —
	// lives INLINE in the `candy:` value.
	must(filepath.Join(dir, "candy", "redis", "charly.yml"), `redis:
  candy:
    version: "2026.150.0000"
    description: in-memory store
    package:
      - redis
    plan:
      - check: the binary exists
        file: /usr/bin/redis-server
`)

	uf, ok, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified rejected cross-FILE name reuse: %v", err)
	}
	if !ok || uf == nil {
		t.Fatal("LoadUnified returned ok=false")
	}
	cfg := uf.ProjectConfig()
	if _, present := cfg.Box["redis"]; !present {
		t.Errorf("box.redis missing; boxes present: %v", boxConfigKeys(cfg))
	}
	cands, err := uf.ProjectCandies(dir)
	if err != nil {
		t.Fatalf("ProjectCandies: %v", err)
	}
	if cands["redis"] == nil {
		t.Errorf("candy.redis missing; got %d candies", len(cands))
	}

	// --- Within ONE document: duplicate top-level name rejected. ---
	dir2 := t.TempDir()
	dupDoc := `version: 2026.204.1223
redis:
  candy:
    base: fedora
redis:
  local:
    candy: [redis]
`
	if err := os.WriteFile(filepath.Join(dir2, "charly.yml"), []byte(dupDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadUnified(dir2); err == nil {
		t.Fatal("LoadUnified accepted a duplicate top-level node name within one document; want a collision error")
	}
}
