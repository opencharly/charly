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

// TestMigrationTable_CompactNodeForm: the table carries the schema-compaction
// migration — an apply: goHook entry that touches host state — as its FIRST
// (oldest) step, ahead of any later step.
func TestMigrationTable_CompactNodeForm(t *testing.T) {
	if len(migrationTable) == 0 {
		t.Fatal("migration table is empty, expected at least the compact-node-form entry")
	}
	m := migrationTable[0]
	if m.Name != "compact-node-form" || m.Apply != "compactNodeForm" || !m.TouchesHost {
		t.Errorf("unexpected first table entry: %+v", m)
	}
	if _, ok := goHooks[m.Apply]; !ok {
		t.Errorf("hook %q not registered in goHooks", m.Apply)
	}
}

// TestMigrationTable_StripCandyLibvirtField: the table carries the candy-level
// `libvirt:` field removal as a project-only (non-touches_host) apply: goHook
// entry, strictly after compact-node-form.
func TestMigrationTable_StripCandyLibvirtField(t *testing.T) {
	if len(migrationTable) != 3 {
		t.Fatalf("migration table should carry exactly 3 entries, got %d", len(migrationTable))
	}
	m := migrationTable[1]
	if m.Name != "strip-candy-libvirt-field" || m.Apply != "stripCandyLibvirtField" || m.TouchesHost {
		t.Errorf("unexpected second table entry: %+v", m)
	}
	if _, ok := goHooks[m.Apply]; !ok {
		t.Errorf("hook %q not registered in goHooks", m.Apply)
	}
	if !migrationTable[0].Version.Less(m.Version) {
		t.Errorf("strip-candy-libvirt-field version %s must be strictly after compact-node-form %s", m.Version, migrationTable[0].Version)
	}
}

// TestMigrationTable_StripDeployShellOverlay: the table carries the deploy-scope
// `shell:` overlay field removal as a touches_host apply: goHook entry (the field
// was authorable on the per-host charly.yml too), strictly after
// strip-candy-libvirt-field.
func TestMigrationTable_StripDeployShellOverlay(t *testing.T) {
	m := migrationTable[2]
	if m.Name != "strip-deploy-shell-overlay" || m.Apply != "stripDeployShellOverlay" || !m.TouchesHost {
		t.Errorf("unexpected third table entry: %+v", m)
	}
	if _, ok := goHooks[m.Apply]; !ok {
		t.Errorf("hook %q not registered in goHooks", m.Apply)
	}
	if !migrationTable[1].Version.Less(m.Version) {
		t.Errorf("strip-deploy-shell-overlay version %s must be strictly after strip-candy-libvirt-field %s", m.Version, migrationTable[1].Version)
	}
}

// TestStripCandyLibvirtField_RemovesCandyLevelOnly: the reshaper removes ONLY the
// direct candy-body `libvirt:` field, leaving a same-named `vm:`-kind entity's own
// domain-config `libvirt: {...}` object AND a `libvirt:` check-verb step nested in
// a candy's `plan:` completely untouched (the exact ambiguity a blanket
// under_kind-scoped delete_key op would have gotten wrong — see the hook's header).
func TestStripCandyLibvirtField_RemovesCandyLevelOnly(t *testing.T) {
	m := migration{Name: "t", Apply: "stripCandyLibvirtField"}
	in := "" +
		"qemu-guest-agent:\n" +
		"  candy:\n" +
		"    version: 2026.149.1200\n" +
		"    package: [qemu-guest-agent]\n" +
		"    libvirt: [\"<channel type='unix'/>\"]\n" +
		"    plan:\n" +
		"      - check: the libvirt domain is queryable\n" +
		"        libvirt: info\n" +
		"vm-libvirt:\n" +
		"  vm:\n" +
		"    libvirt:\n" +
		"      devices:\n" +
		"        channels: [{type: unix}]\n"
	out, changed := applyTransform(t, m, in)
	if !changed {
		t.Fatal("expected the candy-level libvirt: field to be removed")
	}
	var doc struct {
		QemuGuestAgent struct {
			Candy struct {
				Package []string         `yaml:"package"`
				Libvirt []string         `yaml:"libvirt"`
				Plan    []map[string]any `yaml:"plan"`
			} `yaml:"candy"`
		} `yaml:"qemu-guest-agent"`
		VmLibvirt struct {
			Vm struct {
				Libvirt map[string]any `yaml:"libvirt"`
			} `yaml:"vm"`
		} `yaml:"vm-libvirt"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("reparse: %v\n%s", err, out)
	}
	if doc.QemuGuestAgent.Candy.Libvirt != nil {
		t.Errorf("candy-level libvirt: field survived: %v", doc.QemuGuestAgent.Candy.Libvirt)
	}
	if len(doc.QemuGuestAgent.Candy.Package) != 1 || doc.QemuGuestAgent.Candy.Package[0] != "qemu-guest-agent" {
		t.Errorf("unrelated candy field damaged: %v", doc.QemuGuestAgent.Candy.Package)
	}
	if len(doc.QemuGuestAgent.Candy.Plan) != 1 || doc.QemuGuestAgent.Candy.Plan[0]["libvirt"] != "info" {
		t.Errorf("the candy's own plan-step libvirt: check-verb sugar was damaged: %v", doc.QemuGuestAgent.Candy.Plan)
	}
	if doc.VmLibvirt.Vm.Libvirt == nil {
		t.Error("the vm entity's own libvirt: domain config was incorrectly removed")
	}
	if _, changed2 := applyTransform(t, m, out); changed2 {
		t.Error("second pass changed an already-migrated doc")
	}
}

// TestStripDeployShellOverlay_RemovesSequenceValuedOnly: the reshaper removes ONLY
// a direct, SEQUENCE-valued `shell:` key (the retired deploy-scope overlay),
// leaving a candy's own MAPPING-valued `shell:` (#Shell intrinsic init) on a
// DIFFERENT entity completely untouched — the exact ambiguity a blanket
// scope-scoped delete_key op would have gotten wrong (see the hook's header).
func TestStripDeployShellOverlay_RemovesSequenceValuedOnly(t *testing.T) {
	m := migration{Name: "t", Apply: "stripDeployShellOverlay"}
	in := "" +
		"mydeploy:\n" +
		"  pod:\n" +
		"    image: x\n" +
		"    shell:\n" +
		"    - id: direnv\n" +
		"      bash:\n" +
		"        init: direnv hook bash\n" +
		"mycandy:\n" +
		"  candy:\n" +
		"    version: 2026.149.1200\n" +
		"    description: d\n" +
		"    shell:\n" +
		"      init: export FOO=bar\n" +
		"    plan:\n" +
		"    - check: c\n" +
		"      file: /x\n"
	out, changed := applyTransform(t, m, in)
	if !changed {
		t.Fatal("expected the deploy-scope sequence-valued shell: field to be removed")
	}
	var doc struct {
		MyDeploy struct {
			Pod struct {
				Image string           `yaml:"image"`
				Shell []map[string]any `yaml:"shell"`
			} `yaml:"pod"`
		} `yaml:"mydeploy"`
		MyCandy struct {
			Candy struct {
				Shell map[string]any   `yaml:"shell"`
				Plan  []map[string]any `yaml:"plan"`
			} `yaml:"candy"`
		} `yaml:"mycandy"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("reparse: %v\n%s", err, out)
	}
	if doc.MyDeploy.Pod.Shell != nil {
		t.Errorf("deploy-scope sequence-valued shell: survived: %v", doc.MyDeploy.Pod.Shell)
	}
	if doc.MyDeploy.Pod.Image != "x" {
		t.Errorf("unrelated pod field damaged: %v", doc.MyDeploy.Pod.Image)
	}
	if doc.MyCandy.Candy.Shell == nil {
		t.Error("the candy's own mapping-valued shell: field was incorrectly removed")
	}
	if len(doc.MyCandy.Candy.Plan) != 1 || doc.MyCandy.Candy.Plan[0]["file"] != "/x" {
		t.Errorf("the candy's own plan was damaged: %v", doc.MyCandy.Candy.Plan)
	}
	if _, changed2 := applyTransform(t, m, out); changed2 {
		t.Error("second pass changed an already-migrated doc")
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

// TestRunMigrations_ProjectAtHeadOverlayLags: a project already at HEAD must NOT
// short-circuit a LAGGING per-host overlay — the touches_host chain + the
// universal stamp bring the overlay up (the pre-fix behavior left operators in
// an unresolvable "Run: charly migrate" loop: every deploy-state write refused
// the old overlay schema while migrate reported nothing to do).
func TestRunMigrations_ProjectAtHeadOverlayLags(t *testing.T) {
	dir := writeRoot(t, spec.SchemaVersion)
	overlay := filepath.Join(t.TempDir(), "charly.yml")
	body := "version: " + spec.SchemaFloor + "\ngithubrunner:\n    pod:\n        image: githubrunner\n"
	if err := os.WriteFile(overlay, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	changed, err := runMigrations(&MigrateContext{Dir: dir, HostDeployPath: overlay, Out: &out}, false)
	if err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	if !changed {
		t.Fatalf("lagging overlay must be migrated; output: %q", out.String())
	}
	after, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "version: "+spec.SchemaVersion) {
		t.Errorf("overlay not stamped to head:\n%s", after)
	}
	// The project root must be untouched (idempotent chain, no spurious rewrite).
	root, _ := os.ReadFile(filepath.Join(dir, "charly.yml"))
	if !strings.Contains(string(root), "version: "+spec.SchemaVersion) {
		t.Error("project root version changed unexpectedly")
	}
	// Second run: full no-op.
	out.Reset()
	changed, err = runMigrations(&MigrateContext{Dir: dir, HostDeployPath: overlay, Out: &out}, false)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("second run must be a no-op, output: %q", out.String())
	}
}
