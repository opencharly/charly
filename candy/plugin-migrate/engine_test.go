package migrate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// TestEngine_CueOwnedVersion proves the CUE-owned schema version linchpin: the
// generated spec.SchemaVersion / spec.SchemaFloor consts (from schema/version.cue)
// parse to the values kit exposes and the load-time gate uses.
func TestEngine_CueOwnedVersion(t *testing.T) {
	if spec.SchemaVersion != kit.LatestSchemaVersion().String() {
		t.Fatalf("spec.SchemaVersion %q != kit.LatestSchemaVersion() %q", spec.SchemaVersion, kit.LatestSchemaVersion())
	}
	if spec.SchemaFloor != kit.SchemaFloor().String() {
		t.Fatalf("spec.SchemaFloor %q != kit.SchemaFloor() %q", spec.SchemaFloor, kit.SchemaFloor())
	}
}

// TestMigrationTable_CompactNodeForm: the table carries exactly the
// schema-compaction migration — an apply: goHook entry that touches host state.
func TestMigrationTable_CompactNodeForm(t *testing.T) {
	if len(migrationTable) != 1 {
		t.Fatalf("migration table should carry exactly the compact-node-form entry, got %d", len(migrationTable))
	}
	m := migrationTable[0]
	if m.Name != "compact-node-form" || m.Apply != "compactNodeForm" || !m.TouchesHost {
		t.Errorf("unexpected table entry: %+v", m)
	}
	if _, ok := goHooks[m.Apply]; !ok {
		t.Errorf("hook %q not registered in goHooks", m.Apply)
	}
}

func writeRoot(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	body := "version: " + version + "\ndiscover: []\n"
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunMigrations_AtHeadNoOp: a config already at HEAD is a no-op, unchanged.
func TestRunMigrations_AtHeadNoOp(t *testing.T) {
	dir := writeRoot(t, spec.SchemaVersion)
	before, _ := os.ReadFile(filepath.Join(dir, "charly.yml"))
	var out bytes.Buffer
	changed, err := runMigrations(&MigrateContext{Dir: dir, Out: &out}, false)
	if err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	if changed {
		t.Error("at-HEAD config reported changed=true")
	}
	if !strings.Contains(out.String(), "nothing to migrate") {
		t.Errorf("want 'nothing to migrate', got %q", out.String())
	}
	after, _ := os.ReadFile(filepath.Join(dir, "charly.yml"))
	if !bytes.Equal(before, after) {
		t.Error("at-HEAD config was modified")
	}
}

// TestRunMigrations_BelowFloorRefused: a below-floor config is refused with an
// actionable "predates the supported floor" error and left untouched.
func TestRunMigrations_BelowFloorRefused(t *testing.T) {
	dir := writeRoot(t, "2026.001.0000")
	before, _ := os.ReadFile(filepath.Join(dir, "charly.yml"))
	_, err := runMigrations(&MigrateContext{Dir: dir, Out: &bytes.Buffer{}}, false)
	if err == nil {
		t.Fatal("below-floor config accepted; want error")
	}
	if !strings.Contains(err.Error(), "predates the supported floor") {
		t.Errorf("want 'predates the supported floor', got %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "charly.yml"))
	if !bytes.Equal(before, after) {
		t.Error("below-floor config was modified")
	}
}

// TestRunMigrations_NoConfig: an empty dir is a friendly no-op (not an error).
func TestRunMigrations_NoConfig(t *testing.T) {
	changed, err := runMigrations(&MigrateContext{Dir: t.TempDir(), Out: &bytes.Buffer{}}, false)
	if err != nil || changed {
		t.Fatalf("empty dir: changed=%v err=%v", changed, err)
	}
}

// applyTransform runs a migration's transform over a YAML doc string.
func applyTransform(t *testing.T, m migration, in string) (out string, changed bool) {
	t.Helper()
	transform, err := buildTransform(m)
	if err != nil {
		t.Fatalf("buildTransform: %v", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(in), &doc); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	changed = transform(&doc)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		t.Fatalf("encode: %v", err)
	}
	_ = enc.Close()
	return buf.String(), changed
}

// TestOpWalker_RenameKey: rename preserves the value + inline comment, and is a
// no-op on a second pass (the key is already renamed).
func TestOpWalker_RenameKey(t *testing.T) {
	m := migration{Name: "t", Ops: []migrationOp{{Op: "rename_key", From: "widget", To: "gadget", Scope: "any"}}}
	out, changed := applyTransform(t, m, "top:\n  widget: 5 # keep me\n")
	if !changed || !strings.Contains(out, "gadget: 5") || strings.Contains(out, "widget:") {
		t.Errorf("rename failed: %q", out)
	}
	if !strings.Contains(out, "keep me") {
		t.Errorf("inline comment lost: %q", out)
	}
	if _, changed2 := applyTransform(t, m, out); changed2 {
		t.Error("second pass changed an already-migrated doc")
	}
}

// TestOpWalker_DeleteKey: delete removes the pair and carries the deleted key's
// head comment onto the following key.
func TestOpWalker_DeleteKey(t *testing.T) {
	m := migration{Name: "t", Ops: []migrationOp{{Op: "delete_key", Key: "gone", Scope: "root"}}}
	out, changed := applyTransform(t, m, "gone: 1\nkept: 2\n")
	if !changed || strings.Contains(out, "gone:") || !strings.Contains(out, "kept: 2") {
		t.Errorf("delete failed: %q", out)
	}
}

// TestOpWalker_RemapScalarUnderKind: remap flips only the scalar inside the
// under_kind-scoped subtree, leaving an identical key elsewhere untouched.
func TestOpWalker_RemapScalarUnderKind(t *testing.T) {
	m := migration{Name: "t", Ops: []migrationOp{{Op: "remap_scalar", Key: "target", From: "host", To: "local", UnderKind: "bundle"}}}
	out, changed := applyTransform(t, m, "d1:\n  bundle:\n    target: host\nother:\n  target: host\n")
	if !changed {
		t.Fatal("remap reported no change")
	}
	if strings.Count(out, "target: local") != 1 || strings.Count(out, "target: host") != 1 {
		t.Errorf("under_kind scope wrong — want exactly one flipped: %q", out)
	}
}

// TestOpWalker_MoveKey: move relocates a pair between two child mappings.
func TestOpWalker_MoveKey(t *testing.T) {
	m := migration{Name: "t", Ops: []migrationOp{{Op: "move_key", Key: "k", FromParent: "a", ToParent: "b"}}}
	out, changed := applyTransform(t, m, "root:\n  a:\n    k: v\n  b:\n    x: y\n")
	if !changed {
		t.Fatal("move reported no change")
	}
	var got map[string]map[string]map[string]string
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if _, stillInA := got["root"]["a"]["k"]; stillInA {
		t.Error("k still under a")
	}
	if got["root"]["b"]["k"] != "v" {
		t.Errorf("k not moved to b: %v", got["root"]["b"])
	}
}

// TestBuildTransform_UnknownHook: an `apply:` step naming an unregistered Go hook
// is a hard error (the escape-hatch fail-fast).
func TestBuildTransform_UnknownHook(t *testing.T) {
	if _, err := buildTransform(migration{Name: "t", Apply: "nope"}); err == nil {
		t.Fatal("unknown hook accepted; want error")
	}
}

// TestBuildTransform_RegisteredHook: an `apply:` step dispatches to its registered
// goHooks entry.
func TestBuildTransform_RegisteredHook(t *testing.T) {
	const name = "__test_hook_marker"
	called := false
	goHooks[name] = func(*yaml.Node) bool { called = true; return true }
	defer delete(goHooks, name)
	transform, err := buildTransform(migration{Name: "t", Apply: name})
	if err != nil {
		t.Fatalf("buildTransform: %v", err)
	}
	var doc yaml.Node
	_ = yaml.Unmarshal([]byte("a: 1\n"), &doc)
	if !transform(&doc) || !called {
		t.Error("registered hook not dispatched")
	}
}
