package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/charly/charly/spec"
)

// TestLoadUnified_PackageGroupPluginKind proves the FIRST kind→plugin extraction
// end-to-end through the REAL loader: a `package-group:` node (the former core
// builtin kind, now a dedicated plugin unit) is
//
//	(1) RECOGNIZED by the loader as a registered non-core ClassKind discriminator
//	    (classifyDisc → providerRegistry.ResolveKind) and PASSES the #NodeDoc gate
//	    (package-group was removed from the closed #Node arms + _reservedNode);
//	(2) VALIDATED at load time against the plugin's served #PackageGroupInput schema
//	    (runPluginKind → loadBuiltinPluginUnits gate → validateAuthoredPluginInput);
//	(3) DECODED out-of-the-closed-core via Invoke(OpLoad) into
//	    uf.PluginKinds["package-group"]["mygroup"] — NAME-KEYED (the node key is the
//	    storage key), NOT a typed core map (the former core map was removed);
//	(4) CARRIED through mergeUnified (the mergePluginKindsMap merge) so the full
//	    loader path keeps it instead of dropping the per-document `sub`.
//
// The values round-trip: the canonical entity JSON decodes back into spec.Group.
func TestLoadUnified_PackageGroupPluginKind(t *testing.T) {
	dir := t.TempDir()
	doc := `version: "` + latestSchemaVersion.String() + `"
mygroup:
  package-group:
    description: a test netinstall group
    critical: true
    hidden: false
    source: netinstall.yaml
  mygroup-require:
    require: [base, core]
`
	if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified package-group plugin kind: %v", err)
	}

	entities := uf.PluginKinds["package-group"]
	if len(entities) != 1 {
		t.Fatalf("expected 1 package-group entity in uf.PluginKinds, got %d (%v)", len(entities), uf.PluginKinds)
	}
	// Name-keyed: the entity is stored under its node name (the top-level key).
	body, ok := entities["mygroup"]
	if !ok {
		t.Fatalf("expected package-group entity keyed by node name %q, got keys %v", "mygroup", entities)
	}

	// The plugin returns canonical entity JSON; a consumer reads it back into spec.Group.
	var g spec.Group
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode plugin-kind entity JSON into spec.Group: %v", err)
	}
	if g.Description != "a test netinstall group" {
		t.Errorf("description = %q, want %q", g.Description, "a test netinstall group")
	}
	if !g.Critical {
		t.Errorf("critical = %v, want true", g.Critical)
	}
	if g.Hidden {
		t.Errorf("hidden = %v, want false", g.Hidden)
	}
	if g.Source != "netinstall.yaml" {
		t.Errorf("source = %q, want %q", g.Source, "netinstall.yaml")
	}
	// The non-scalar `require` field, authored as a folded child node, round-trips.
	if len(g.Require) != 2 || g.Require[0] != "base" || g.Require[1] != "core" {
		t.Errorf("require = %v, want [base core]", g.Require)
	}
}

// TestLoadUnified_PluginKindNameOverride proves Cutover A end-to-end through the REAL
// loader: two documents in one charly.yml each author a `package-group:` named "dup".
// Under name-keyed root-wins storage they collapse to ONE entry — the FIRST (root)
// document wins — instead of the two appended entries the pre-cutover nameless list
// produced. This is the property Cutover B's sidecar/agent extraction relies on (a
// project entity overriding an embedded/imported one of the same name).
func TestLoadUnified_PluginKindNameOverride(t *testing.T) {
	dir := t.TempDir()
	doc := `version: "` + latestSchemaVersion.String() + `"
dup:
  package-group:
    description: first wins
---
dup:
  package-group:
    description: second loses
`
	if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified plugin-kind override: %v", err)
	}

	entities := uf.PluginKinds["package-group"]
	if len(entities) != 1 {
		t.Fatalf("expected 1 package-group entity after name-keyed override, got %d (%v)", len(entities), entities)
	}
	body, ok := entities["dup"]
	if !ok {
		t.Fatalf("expected entity keyed by node name %q, got keys %v", "dup", entities)
	}
	var g spec.Group
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("decode override entity into spec.Group: %v", err)
	}
	if g.Description != "first wins" {
		t.Errorf("root-wins override failed: description = %q, want %q (the first/root document)", g.Description, "first wins")
	}
}

// TestLoadUnified_PackageGroupRejectsBadInput proves the load-time schema gate:
// the plugin's served #PackageGroupInput validates the authored entity body, so a
// type-violating field is a hard load error (not a silent drop).
func TestLoadUnified_PackageGroupRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	doc := `version: "` + latestSchemaVersion.String() + `"
mygroup:
  package-group:
    critical: "not-a-bool"
`
	if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadUnified(dir); err == nil {
		t.Fatal("expected a load error for a non-bool critical:, got nil")
	}
}
