package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadUnified_NodeForm_Discovery proves discovered node-form candy + box
// manifests load: a candy dir (lazy From → scanCandy → node-form parseCandyYAML)
// and a box dir (normalized inline).
func TestLoadUnified_NodeForm_Discovery(t *testing.T) {
	dir := t.TempDir()
	must := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(dir, "charly.yml"), `version: "`+latestSchemaVersion.String()+`"
discover:
  - candy
  - box
`)
	must(filepath.Join(dir, "candy", "redis", "charly.yml"), `redis:
  candy:
    version: "2026.150.0000"
    description: in-memory store
  redis-step-0:
    check: the binary exists
    file: /usr/bin/redis-server
`)
	must(filepath.Join(dir, "box", "coder", "charly.yml"), `coder:
  candy:
    base: fedora
  coder-candy:
    candy: [redis]
`)
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified with node-form discovery: %v", err)
	}
	cfg := uf.ProjectConfig()
	cands, err := uf.ProjectCandies(dir)
	if err != nil {
		t.Fatalf("ProjectCandies: %v", err)
	}
	if cands["redis"] == nil {
		t.Errorf("discovered node-form candy redis not loaded; got %d candies", len(cands))
	} else if cands["redis"].Version != "2026.150.0000" {
		t.Errorf("redis candy version = %q", cands["redis"].Version)
	}
	if _, ok := cfg.Box["coder"]; !ok {
		t.Errorf("discovered node-form box coder not loaded; boxes present: %v", boxConfigKeys(cfg))
	}
}

func boxConfigKeys(c *Config) []string {
	out := make([]string, 0, len(c.Box))
	for k := range c.Box {
		out = append(out, k)
	}
	return out
}
