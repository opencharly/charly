package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/spec"
)

// testResolvedBox returns a ResolvedBox suitable for feeding the
// task emitters. Uses fedora (rpm) by default with UID/GID 1000.
func testResolvedBox() *buildkit.ResolvedBox {
	return &buildkit.ResolvedBox{
		Name:         "test-img",
		User:         "user",
		UID:          1000,
		GID:          1000,
		Home:         "/home/user",
		Pkg:          "rpm",
		BuildFormats: []string{"rpm"},
		Tags:         []string{"all", "rpm"},
		DistroDef:    testDistroDef("fedora"),
	}
}

// --- Task.Kind() — exactly-one-verb enforcement ---

func TestTaskKind_Valid(t *testing.T) {
	cases := []struct {
		task spec.Op
		want string
	}{
		{cmdOp("echo hi"), "plugin"}, // command is a plugin verb now (plugin: command)
		{spec.Op{Mkdir: "/etc/foo"}, "mkdir"},
		{spec.Op{Copy: "foo", To: "/bar"}, "copy"},
		{spec.Op{Write: "/x", Content: "body"}, "write"},
		{spec.Op{Link: "/a", Target: "/b"}, "link"},
		{spec.Op{Download: "http://x"}, "download"},
		{spec.Op{Setcap: "/bin/x"}, "setcap"},
		{spec.Op{Build: "all"}, "build"},
	}
	for _, c := range cases {
		got, err := c.task.Kind()
		if err != nil {
			t.Errorf("Kind(%+v) error: %v", c.task, err)
		}
		if got != c.want {
			t.Errorf("Kind(%+v) = %q, want %q", c.task, got, c.want)
		}
	}
}

// Zero-verb and multiple-verb enforcement on the unified Op.Kind() is covered
// by TestCheck_Kind in checkspec_test.go (one Kind() implementation, one set of
// tests — R3). TestTaskKind_Valid above covers the install-verb names that
// TestCheck_Kind's probe-verb cases do not.
//
// The pure var-substitution / user-resolution / inline-staging / per-verb
// Containerfile-line emitter tests (TaskSubstAutoExports, TaskSubstPath,
// TaskUnresolvedRefs, ResolveUserSpec, StageInlineContent, EmitMkdirBatch,
// EmitCopy, EmitWrite, EmitLinkBatch, EmitSetcapBatch, EmitDownload,
// TaskCacheMounts, EmitCmd, EmitVarsEnv) moved to
// sdk/deploykit/tasks_emit_test.go alongside the K3 alias dissolution — they
// exercise deploykit.* directly now, no core Generator/Config needed.

// --- emitTasks orchestrator ---

func TestEmitTasks_UserCoalescing(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	ops := []spec.Op{
		{Mkdir: "/a", RunAs: "root"},
		{Mkdir: "/b", RunAs: "root"},
		{Mkdir: "/c", RunAs: "root"}, // all root → single USER 0 header, one RUN
	}
	layer := testCandy("lyr", spec.CandyModel{}, spec.CandyView{})
	var b strings.Builder
	_, err := g.toDeploykit().EmitTasks(&b, layer, testResolvedBox(), ops, dir, ".build/test-img")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	// No USER directive should be emitted (running user was "0" already)
	if strings.Contains(out, "USER") {
		t.Errorf("no USER directive expected when starting user matches task user:\n%s", out)
	}
	if strings.Count(out, "RUN") != 1 {
		t.Errorf("three mkdirs should coalesce to one RUN:\n%s", out)
	}
}

// Regression: a command: task must emit a RUN through the emitTasks verb
// switch. The cmd→command rename left the switch on the old "cmd" verb name,
// so command tasks hit default and emitted nothing in the OCI build — silently
// dropping e.g. the rpmfusion repo-enable task and breaking downstream package
// installs. (The existing TestEmitCmd_* call emitCmd directly, bypassing the
// switch, so they did not catch it.)
func TestEmitTasks_CommandEmitsRun(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	ops := []spec.Op{
		{Plugin: "command", PluginInput: map[string]any{"command": "echo rpmfusion-enable"}, RunAs: "root"},
	}
	layer := testCandy("lyr", spec.CandyModel{}, spec.CandyView{})
	var b strings.Builder
	_, err := g.toDeploykit().EmitTasks(&b, layer, testResolvedBox(), ops, dir, ".build/test-img")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "RUN") || !strings.Contains(out, "echo rpmfusion-enable") {
		t.Errorf("command task must emit a RUN in the OCI build, got:\n%s", out)
	}
}

func TestEmitTasks_UserSwitches(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	ops := []spec.Op{
		{Mkdir: "/a", RunAs: "root"},
		{Mkdir: "/b", RunAs: "${USER}"},
		{Mkdir: "/c", RunAs: "${USER}"}, // coalesces with previous
	}
	layer := testCandy("lyr", spec.CandyModel{}, spec.CandyView{})
	var b strings.Builder
	_, err := g.toDeploykit().EmitTasks(&b, layer, testResolvedBox(), ops, dir, ".build/test-img")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	// One USER switch (root → user); no second switch within the user group
	if strings.Count(out, "USER ") != 1 {
		t.Errorf("expected 1 USER switch, got %d:\n%s", strings.Count(out, "USER "), out)
	}
	if !strings.Contains(out, "USER 1000") {
		t.Errorf("should switch to USER 1000 (numeric form from ${USER}):\n%s", out)
	}
	// Two RUN mkdir (one for root, one for user — NOT coalesced across users)
	if strings.Count(out, "mkdir") != 2 {
		t.Errorf("expected 2 mkdir (across users):\n%s", out)
	}
}

func TestEmitTasks_OrderPreserved(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	// mkdir → copy → mkdir sequence: the second mkdir must NOT merge with the first
	ops := []spec.Op{
		{Mkdir: "/a", RunAs: "root"},
		{Copy: "f", To: "/a/f", RunAs: "root"},
		{Mkdir: "/b", RunAs: "root"},
	}
	layer := testCandy("lyr", spec.CandyModel{}, spec.CandyView{})
	var b strings.Builder
	_, err := g.toDeploykit().EmitTasks(&b, layer, testResolvedBox(), ops, dir, ".build/test-img")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	// Check ordering: mkdir /a must come before COPY, COPY before mkdir /b
	idx1 := strings.Index(out, "mkdir -p /a")
	idxCopy := strings.Index(out, "COPY")
	idx2 := strings.Index(out, "mkdir -p /b")
	if idx1 < 0 || idxCopy < 0 || idx2 < 0 {
		t.Fatalf("missing directive: mkdir1=%d copy=%d mkdir2=%d\n%s", idx1, idxCopy, idx2, out)
	}
	if idx1 >= idxCopy || idxCopy >= idx2 {
		t.Errorf("order violated: mkdir1=%d copy=%d mkdir2=%d\n%s", idx1, idxCopy, idx2, out)
	}
}

func TestEmitTasks_ParentDirAutoInsert(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	ops := []spec.Op{
		// Copy to /etc/traefik/traefik.yml without declaring /etc/traefik first
		{Copy: "traefik.yml", To: "/etc/traefik/traefik.yml", RunAs: "root"},
	}
	layer := testCandy("lyr", spec.CandyModel{}, spec.CandyView{})
	var b strings.Builder
	_, err := g.toDeploykit().EmitTasks(&b, layer, testResolvedBox(), ops, dir, ".build/test-img")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	// auto-inserted mkdir -p /etc/traefik before COPY
	idxMkdir := strings.Index(out, "mkdir -p /etc/traefik")
	idxCopy := strings.Index(out, "COPY")
	if idxMkdir < 0 {
		t.Errorf("expected auto-inserted parent mkdir:\n%s", out)
	}
	if idxCopy < idxMkdir {
		t.Errorf("parent mkdir must precede COPY:\n%s", out)
	}
}

func TestEmitTasks_ParentDirSuppressedWhenDeclared(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	// Author explicitly declared /etc/foo via mkdir — no auto-insert
	ops := []spec.Op{
		{Mkdir: "/etc/foo", RunAs: "root"},
		{Copy: "bar", To: "/etc/foo/bar", RunAs: "root"},
	}
	layer := testCandy("lyr", spec.CandyModel{}, spec.CandyView{})
	var b strings.Builder
	_, err := g.toDeploykit().EmitTasks(&b, layer, testResolvedBox(), ops, dir, ".build/test-img")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	// Only ONE mkdir RUN (the author's) — no auto-insert duplicate
	if strings.Count(out, "mkdir -p /etc/foo") != 1 {
		t.Errorf("should not auto-insert parent dir already declared by author:\n%s", out)
	}
}

func TestEmitTasks_WriteStagesContent(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	ops := []spec.Op{
		{Write: "/etc/foo.conf", Content: "hello world\n", RunAs: "root"},
	}
	layer := testCandy("lyr", spec.CandyModel{}, spec.CandyView{})
	var b strings.Builder
	buildDir := filepath.Join(dir, "test-img")
	_, err := g.toDeploykit().EmitTasks(&b, layer, testResolvedBox(), ops, buildDir, ".build/test-img")
	if err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "COPY --chmod=0644 .build/test-img/_inline/lyr/") {
		t.Errorf("expected COPY from staged inline path:\n%s", out)
	}
	// Content file exists on disk
	entries, _ := os.ReadDir(filepath.Join(buildDir, "_inline", "lyr"))
	if len(entries) != 1 {
		t.Errorf("expected one staged file, got %d", len(entries))
	}
}

// --- Validator ---
//
// The five former validateCandyTasks host tests (CopyRequiresTo, UnresolvedVar, ReservedVarKey,
// BuildOnlyAll, HappyPath) moved with the validateCandyTasks rule to candy/plugin-box (task #60).
// They are re-expressed as on-disk fixtures driven through the real `charly box validate` gate in
// validate_fixture_test.go (TestValidate_Task*). The bad-mode (octal ^0[0-7]{3,4}$) rejection is a
// CUE concern (#Op.mode) — see cue_tighten_test.go "candy run step bad mode rejected".

// --- Parity: ensure HasInstallFiles picks up HasTasks ---

// TestCandy_HasInstallFiles_IncludesTasks proves the RunOps host-completion pass
// (completeCandyRunOps, charly/layers.go) folds a candy's `run:` steps into RunOps and
// OR-completes HasInstallFiles/HasContent with it — the associative-OR completion
// scanFromParsed's own doc comment defers to the host (RunOps needs opInContext, which a
// single candy's scan can't reach). Every ScanCandy/ScanAllCandy/ProjectCandies entry point
// calls completeCandyRunOps before the final FinalizeCandyRefs+wrap, so this exercises that
// SAME completion step directly on a (Model, View) pair, mirroring what those call sites do.
func TestCandy_HasInstallFiles_IncludesTasks(t *testing.T) {
	m := spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}}
	v := spec.CandyView{}
	completeCandyRunOps(&m, &v)
	l := testCandy("x", m, v)
	if !l.HasTasks() {
		t.Fatal("HasTasks() should be true after completeCandyRunOps folds the run: step into RunOps")
	}
	if !l.HasInstallFiles() {
		t.Error("HasInstallFiles() should be true when HasTasks is true")
	}
}
