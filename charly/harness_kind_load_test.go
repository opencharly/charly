package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSkillKind_CompiledInStackedNodes proves the U0 keystone END-TO-END: a `skill:` kind entity
// whose serving plugin (candy/plugin-harness-kind) is COMPILED-IN is recognized at parse (the
// init() registration feeds the Threaded.Kinds snapshot → loaderkit.classifyKind admits it
// top-level), validated at load against the served #SkillInput schema (validateAuthoredPluginInput),
// decoded by runPluginKind into uf.PluginKinds["skill"], and — the load-bearing layout question —
// a SINGLE charly.yml stacks a `candy:` node and a sibling `skill:` node (ParseDoc iterates every
// top-level node, each with its own single discriminator; only node names are document-unique).
func TestSkillKind_CompiledInStackedNodes(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	dir := t.TempDir()
	// One file: a candy node + a sibling skill node (the physical layout the migration uses —
	// per-candy skills live in the owning candy's own charly.yml).
	rootYAML := `version: ` + LatestSchemaVersion().String() + `
discover:
    - path: candy
      recursive: true
postgresql:
    candy:
        version: 2026.218.1200
        description: Postgres 16 + contrib.
        plan:
            - check: /usr/bin/postgres exists
postgresql-skill:
    skill:
        name: postgresql
        family: charly-infrastructure
        owner: postgresql
        description: Use when working with postgresql.
        content: |
            # Postgresql
            Start, configure, and probe a postgresql service.
        triggers:
            - "postgres / postgresql / pg"
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(rootYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified must parse a candy node + a sibling skill node in one charly.yml: %v", err)
	}
	// The candy folds into uf.Candy; the skill folds into uf.PluginKinds["skill"].
	if _, ok := uf.Candy["postgresql"]; !ok {
		t.Fatalf("candy node postgresql not folded into uf.Candy")
	}
	byName, ok := uf.PluginKinds["skill"]
	if !ok {
		t.Fatalf("no uf.PluginKinds[skill] (the skill kind did not decode); have kinds %v", pluginKindKeys(uf))
	}
	got, ok := byName["postgresql-skill"]
	if !ok {
		t.Fatalf("skill entity postgresql-skill not decoded; have %v", byName)
	}
	body := string(got)
	for _, want := range []string{"postgresql", "charly-infrastructure", "# Postgresql", "postgres / postgresql / pg"} {
		if !strings.Contains(body, want) {
			t.Fatalf("decoded skill body missing %q:\n%s", want, body)
		}
	}
}

// TestDocsKind_CompiledInStackedNodes proves the `docs` kind — the 4th harness-surface corpus
// kind — is admitted exactly like skill/hook/marketplace (R3, no special-casing): a `docs:`
// entity whose serving plugin (candy/plugin-harness-kind, the SAME compiled-in unit) is
// recognized at parse (the init() registration feeds the Threaded.Kinds snapshot →
// loaderkit.classifyKind admits `docs` top-level), validated at load against the served
// #DocsInput schema (validateAuthoredPluginInput), and decoded by runPluginKind into
// uf.PluginKinds["docs"]. A SINGLE charly.yml stacks a `candy:` node and a sibling `docs:`
// node (ParseDoc iterates every top-level node, each with its own single discriminator).
func TestDocsKind_CompiledInStackedNodes(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	dir := t.TempDir()
	// The docs body mirrors the repo's TOP-LEVEL project charly.yml docs node (the
	// #DocsConfig contract exercised by the spec round-trip tests).
	rootYAML := `version: ` + LatestSchemaVersion().String() + `
discover:
    - path: candy
      recursive: true
docs:
    docs:
        sources:
            compiled:
                enabled: true
                compiled_plugins_path: charly/charly.yml
                go_mod_path: charly/go.mod
            release_repos: [plugin-review, plugin-pipeline]
            extra_repos: [plugin-gh]
        marketplace:
            path: marketplace
        projections:
            recipes: true
            cli: true
            candy: true
        output:
            hand_authored: [start, concepts, guides]
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(rootYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified must parse a docs node: %v", err)
	}
	byName, ok := uf.PluginKinds["docs"]
	if !ok {
		t.Fatalf("no uf.PluginKinds[docs] (the docs kind did not decode); have kinds %v", pluginKindKeys(uf))
	}
	got, ok := byName["docs"]
	if !ok {
		t.Fatalf("docs entity not decoded; have %v", byName)
	}
	body := string(got)
	for _, want := range []string{"compiled_plugins_path", "plugin-review", "hand_authored"} {
		if !strings.Contains(body, want) {
			t.Fatalf("decoded docs body missing %q:\n%s", want, body)
		}
	}
}

// TestDocsKind_RejectsInvalidBody proves the served-schema load gate mirrors
// skill/hook/marketplace: a docs body violating #DocsInput (here: an unknown top-level
// field against the closed def) is rejected at load, not silently accepted.
func TestDocsKind_RejectsInvalidBody(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	dir := t.TempDir()
	rootYAML := `version: ` + LatestSchemaVersion().String() + `
discover:
    - path: candy
      recursive: true
docs:
    docs:
        sources:
            compiled:
                enabled: true
        bogus_field: true
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(rootYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadUnified(dir)
	if err == nil {
		t.Fatal("LoadUnified must FAIL when a docs body violates #DocsInput (unknown field)")
	}
	if !strings.Contains(err.Error(), "docs") {
		t.Fatalf("error %q must name the failing docs entity", err)
	}
}

// TestSkillKind_RejectsInvalidBody proves the served-schema load gate: a skill body violating
// #SkillInput (here: an empty content) is rejected at load, not silently accepted.
func TestSkillKind_RejectsInvalidBody(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	dir := t.TempDir()
	rootYAML := `version: ` + LatestSchemaVersion().String() + `
discover:
    - path: candy
      recursive: true
bad-skill:
    skill:
        name: ""
        family: charly-infrastructure
        owner: postgresql
        description: Use when working with postgresql.
        content: ""
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(rootYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadUnified(dir)
	if err == nil {
		t.Fatal("LoadUnified must FAIL when a skill body violates #SkillInput (empty name/content)")
	}
	if !strings.Contains(err.Error(), "postgresql") && !strings.Contains(err.Error(), "skill") {
		t.Fatalf("error %q must name the failing skill entity", err)
	}
}
