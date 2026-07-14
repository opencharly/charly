package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testResolvedBox returns a ResolvedBox suitable for feeding the
// task emitters. Uses fedora (rpm) by default with UID/GID 1000.
func testResolvedBox() *ResolvedBox {
	return &ResolvedBox{
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
		task Op
		want string
	}{
		{cmdOp("echo hi"), "plugin"}, // command is a plugin verb now (plugin: command)
		{Op{Mkdir: "/etc/foo"}, "mkdir"},
		{Op{Copy: "foo", To: "/bar"}, "copy"},
		{Op{Write: "/x", Content: "body"}, "write"},
		{Op{Link: "/a", Target: "/b"}, "link"},
		{Op{Download: "http://x"}, "download"},
		{Op{Setcap: "/bin/x"}, "setcap"},
		{Op{Build: "all"}, "build"},
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

// --- Variable substitution ---

func TestTaskSubstAutoExports(t *testing.T) {
	img := testResolvedBox()
	cases := []struct {
		in, want string
	}{
		{"${USER}", "user"},
		{"${UID}", "1000"},
		{"${GID}", "1000"},
		{"${HOME}", "/home/user"},
		{"hello ${USER}!", "hello user!"},
		{"${UNKNOWN}", "${UNKNOWN}"}, // left alone
		{"${USER}/${HOME}", "user//home/user"},
	}
	for _, c := range cases {
		got := taskSubstAutoExports(c.in, img)
		if got != c.want {
			t.Errorf("taskSubstAutoExports(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTaskSubstPath_TildeExpansion(t *testing.T) {
	img := testResolvedBox()
	got := taskSubstPath("~/.local/bin", img)
	if got != "/home/user/.local/bin" {
		t.Errorf("tilde expansion: got %q", got)
	}
}

func TestTaskUnresolvedRefs(t *testing.T) {
	known := taskKnownNames(map[string]string{"MY_VAR": "x"})
	refs := taskUnresolvedRefs("${MY_VAR}/${USER}/${MISSING}/${NOPE}", known)
	if len(refs) != 2 {
		t.Fatalf("expected 2 unresolved, got %d: %v", len(refs), refs)
	}
	// Order preserved, duplicates deduped
	if refs[0] != "MISSING" || refs[1] != "NOPE" {
		t.Errorf("unresolved = %v, want [MISSING NOPE]", refs)
	}
}

// --- User resolution ---

func TestResolveUserSpec(t *testing.T) {
	img := testResolvedBox()
	cases := []struct {
		in, wantDirective, wantChown string
	}{
		{"", "0", ""},
		{"root", "0", ""},
		{"0", "0", ""},
		{"${USER}", "1000", "1000:1000"},
		{"1000:1000", "1000:1000", "1000:1000"},
		{"500", "500", "500:500"},
		{"postgres", "postgres", "postgres:postgres"},
	}
	for _, c := range cases {
		gotDir, gotCh := resolveUserSpec(c.in, img)
		if gotDir != c.wantDirective || gotCh != c.wantChown {
			t.Errorf("resolveUserSpec(%q) = (%q, %q), want (%q, %q)",
				c.in, gotDir, gotCh, c.wantDirective, c.wantChown)
		}
	}
}

// --- Inline content staging ---

func TestStageInlineContent_Idempotent(t *testing.T) {
	dir := t.TempDir()
	buildDir := filepath.Join(dir, ".build", "img")
	ctx := ".build/img"

	rel1, err := stageInlineContent(buildDir, ctx, "lyr", "hello\n")
	if err != nil {
		t.Fatalf("stage 1: %v", err)
	}
	rel2, err := stageInlineContent(buildDir, ctx, "lyr", "hello\n")
	if err != nil {
		t.Fatalf("stage 2: %v", err)
	}
	if rel1 != rel2 {
		t.Errorf("non-idempotent path: %q vs %q", rel1, rel2)
	}
	if !strings.HasPrefix(rel1, ".build/img/_inline/lyr/") {
		t.Errorf("bad rel path: %q", rel1)
	}
	// Different content → different hash
	rel3, err := stageInlineContent(buildDir, ctx, "lyr", "different\n")
	if err != nil {
		t.Fatalf("stage 3: %v", err)
	}
	if rel3 == rel1 {
		t.Error("different content should produce different hash path")
	}
	// File actually exists
	abs := filepath.Join(dir, rel1)
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("reading staged file: %v", err)
	}
	if string(data) != "hello\n" {
		t.Errorf("staged content mismatch: %q", data)
	}
}

// --- Emitters ---

func TestEmitMkdirBatch_Coalesces(t *testing.T) {
	var b strings.Builder
	tasks := []Op{
		{Mkdir: "/a", RunAs: "root"},
		{Mkdir: "/b", RunAs: "root"},
		{Mkdir: "/c", RunAs: "root"},
	}
	emitMkdirBatch(&b, tasks, testResolvedBox())
	out := b.String()
	if !strings.Contains(out, "RUN mkdir -p /a /b /c") {
		t.Errorf("expected coalesced mkdir, got:\n%s", out)
	}
	// Only one RUN line
	if strings.Count(out, "RUN") != 1 {
		t.Errorf("expected 1 RUN, got %d\n%s", strings.Count(out, "RUN"), out)
	}
}

func TestEmitMkdirBatch_PerModeChmod(t *testing.T) {
	var b strings.Builder
	tasks := []Op{
		{Mkdir: "/a", Mode: "0700"},
		{Mkdir: "/b"}, // default — no chmod
		{Mkdir: "/c", Mode: "0700"},
	}
	emitMkdirBatch(&b, tasks, testResolvedBox())
	out := b.String()
	if !strings.Contains(out, "mkdir -p /a /b /c") {
		t.Errorf("mkdir missing paths:\n%s", out)
	}
	if !strings.Contains(out, "chmod 0700 /a /c") {
		t.Errorf("chmod should group by mode:\n%s", out)
	}
}

func TestEmitCopy_WithChown(t *testing.T) {
	var b strings.Builder
	emitCopy(&b,
		Op{Copy: "wrapper", To: "/home/user/.local/bin/wrapper", Mode: "0755", RunAs: "${USER}"},
		"my-layer", testResolvedBox(),
	)
	out := b.String()
	if !strings.Contains(out, "--from=my-layer") {
		t.Errorf("missing layer stage reference:\n%s", out)
	}
	if !strings.Contains(out, "--chmod=0755") {
		t.Errorf("missing chmod:\n%s", out)
	}
	if !strings.Contains(out, "--chown=1000:1000") {
		t.Errorf("missing chown for ${USER} (should resolve to numeric UID:GID):\n%s", out)
	}
	if !strings.Contains(out, "wrapper /home/user/.local/bin/wrapper") {
		t.Errorf("missing src/dest:\n%s", out)
	}
}

func TestEmitCopy_RootNoChown(t *testing.T) {
	var b strings.Builder
	emitCopy(&b,
		Op{Copy: "traefik.yml", To: "/etc/traefik/traefik.yml", Mode: "0644", RunAs: "root"},
		"traefik", testResolvedBox(),
	)
	out := b.String()
	if strings.Contains(out, "--chown") {
		t.Errorf("root should not emit --chown:\n%s", out)
	}
}

func TestEmitWrite_UsesStagedPath(t *testing.T) {
	var b strings.Builder
	emitWrite(&b,
		Op{Write: "/etc/foo.conf", Content: "body", Mode: "0644", RunAs: "root"},
		".build/img/_inline/lyr/abc123",
		testResolvedBox(),
	)
	out := b.String()
	if !strings.Contains(out, "COPY --chmod=0644 .build/img/_inline/lyr/abc123 /etc/foo.conf") {
		t.Errorf("write should COPY from staged path:\n%s", out)
	}
	// root: no chown
	if strings.Contains(out, "--chown") {
		t.Errorf("root write should not emit --chown:\n%s", out)
	}
}

func TestEmitLinkBatch(t *testing.T) {
	var b strings.Builder
	tasks := []Op{
		{Link: "/usr/local/bin/node", Target: "/usr/bin/node-24"},
		{Link: "/usr/local/bin/npm", Target: "/usr/bin/npm-24"},
	}
	emitLinkBatch(&b, tasks, testResolvedBox())
	out := b.String()
	if !strings.Contains(out, "ln -sf /usr/bin/node-24 /usr/local/bin/node") {
		t.Errorf("missing first link:\n%s", out)
	}
	if !strings.Contains(out, "ln -sf /usr/bin/npm-24 /usr/local/bin/npm") {
		t.Errorf("missing second link:\n%s", out)
	}
	if strings.Count(out, "RUN") != 1 {
		t.Errorf("links should coalesce to one RUN:\n%s", out)
	}
}

func TestEmitSetcapBatch_StripAndSet(t *testing.T) {
	var b strings.Builder
	tasks := []Op{
		{Setcap: "/usr/bin/sway"}, // strip
		{Setcap: "/usr/bin/newuidmap", Caps: "cap_setuid=ep"},
	}
	emitSetcapBatch(&b, tasks, testResolvedBox())
	out := b.String()
	if !strings.Contains(out, "setcap -r /usr/bin/sway") {
		t.Errorf("strip should use -r:\n%s", out)
	}
	if !strings.Contains(out, "setcap cap_setuid=ep /usr/bin/newuidmap") {
		t.Errorf("set should include caps:\n%s", out)
	}
	if strings.Count(out, "RUN") != 1 {
		t.Errorf("setcap should coalesce to one RUN:\n%s", out)
	}
}

func TestEmitDownload_TarGz(t *testing.T) {
	var b strings.Builder
	err := emitDownload(&b,
		Op{
			Download:       "https://example.com/app.tar.gz",
			Extract:        "tar.gz",
			To:             "/usr/local/bin",
			ExtractInclude: []string{"app"},
		},
		testResolvedBox(),
	)
	if err != nil {
		t.Fatalf("emitDownload: %v", err)
	}
	out := b.String()
	// Now extracts from the content-addressed cache file ($__c), not a stream.
	if !strings.Contains(out, `tar -xzf "$__c" -C /usr/local/bin app`) {
		t.Errorf("missing tar -xzf from cache file with include filter:\n%s", out)
	}
	if !strings.Contains(out, "BUILD_ARCH=$(uname -m)") {
		t.Errorf("should set BUILD_ARCH from uname:\n%s", out)
	}
	// The file must actually be CACHED: content-addressed path under the mount,
	// fetched only when absent, atomically renamed from .part on success.
	if !strings.Contains(out, "/tmp/downloads/$(printf %s") || !strings.Contains(out, "sha256sum") {
		t.Errorf("download must be content-addressed in /tmp/downloads:\n%s", out)
	}
	if !strings.Contains(out, `[ -s "$__c" ] ||`) {
		t.Errorf("download must skip re-fetch when the cached file already exists:\n%s", out)
	}
	if !strings.Contains(out, `-o "$__c.part"`) || !strings.Contains(out, `mv -f "$__c.part" "$__c"`) {
		t.Errorf("download must be integrity-safe (.part + atomic rename):\n%s", out)
	}
	if !strings.Contains(out, "--mount=type=cache,id=charly-tmp-downloads,dst=/tmp/downloads") {
		t.Errorf("should declare the downloads cache mount:\n%s", out)
	}
}

func TestEmitDownload_Sh(t *testing.T) {
	var b strings.Builder
	err := emitDownload(&b,
		Op{Download: "https://sh.install", Extract: "sh", Env: map[string]string{"UV_INSTALL_DIR": "/usr/local/bin"}},
		testResolvedBox(),
	)
	if err != nil {
		t.Fatalf("emitDownload: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "UV_INSTALL_DIR=") {
		t.Errorf("should include env var assignment:\n%s", out)
	}
	if !strings.Contains(out, "/usr/local/bin") {
		t.Errorf("should include env value:\n%s", out)
	}
	// The install script is now cached then run from the cache file: the env
	// vars precede `sh "$__c"` so the SCRIPT sees them.
	if !strings.Contains(out, `sh "$__c"`) {
		t.Errorf("should run the cached install script:\n%s", out)
	}
	if !strings.Contains(out, "sha256sum") || !strings.Contains(out, "/tmp/downloads") {
		t.Errorf("install script should also be content-addressed in the cache:\n%s", out)
	}
	idxSh := strings.LastIndex(out, `sh "$__c"`)
	idxEnv := strings.LastIndex(out, "UV_INSTALL_DIR=")
	if idxEnv > idxSh {
		t.Errorf("env vars should appear BEFORE `sh \"$__c\"` so the script sees them:\n%s", out)
	}
}

func TestEmitDownload_CacheModifier(t *testing.T) {
	// A download task can declare extra `cache:` mounts (e.g. a build cache),
	// owned per the task user. Root task → shared mount; user task → owned.
	var b strings.Builder
	if err := emitDownload(&b,
		Op{Download: "https://x/app.zip", Extract: "zip", To: "/opt/app", RunAs: "root",
			Cache: []string{"/var/cache/app-build"}},
		testResolvedBox()); err != nil {
		t.Fatalf("emitDownload: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "--mount=type=cache,id=charly-var-cache-app-build,dst=/var/cache/app-build,sharing=locked") {
		t.Errorf("root download task should get a SHARED cache mount for cache:\n%s", out)
	}
	if !strings.Contains(out, `unzip -o "$__c" -d /opt/app`) {
		t.Errorf("zip should extract from the cache file:\n%s", out)
	}
}

func TestTaskCacheMounts_OwnershipByUser(t *testing.T) {
	img := testResolvedBox() // UID/GID 1000 in the test fixture
	// root task → shared (sharing=locked), no uid in id
	root := taskCacheMounts(Op{RunAs: "root", Cache: []string{"/var/cache/x"}}, img)
	if len(root) != 1 || !strings.Contains(root[0], "sharing=locked") || strings.Contains(root[0], "uid=") {
		t.Errorf("root cache mount should be shared (no uid): %v", root)
	}
	// user task → owned (uid/gid), id carries -uid<N>
	user := taskCacheMounts(Op{RunAs: "${USER}", Cache: []string{"/var/cache/x"}}, img)
	if len(user) != 1 || !strings.Contains(user[0], "uid=") || !strings.Contains(user[0], "-uid") {
		t.Errorf("non-root cache mount should be uid-owned: %v", user)
	}
	// no cache: → no mounts
	if got := taskCacheMounts(cmdOp("x"), img); got != nil {
		t.Errorf("no cache: should yield nil, got %v", got)
	}
}

func TestEmitDownload_UnknownExtract(t *testing.T) {
	var b strings.Builder
	err := emitDownload(&b, Op{Download: "http://x", Extract: "rar"}, testResolvedBox())
	if err == nil {
		t.Fatal("expected error for unknown extract")
	}
}

func TestEmitCmd_RootCacheMounts(t *testing.T) {
	var b strings.Builder
	emitCmd(&b,
		Op{Plugin: "command", PluginInput: map[string]any{"command": "echo hello"}, RunAs: "root"},
		"my-layer", testResolvedBox(), true,
	)
	out := b.String()
	if !strings.Contains(out, "--mount=type=bind,from=my-layer") {
		t.Errorf("should bind-mount layer stage at /ctx:\n%s", out)
	}
	if !strings.Contains(out, "libdnf5") {
		t.Errorf("root cmd should include distro format cache:\n%s", out)
	}
	if !strings.Contains(out, "set -e") {
		t.Errorf("should include set -e:\n%s", out)
	}
	if !strings.Contains(out, "BUILD_ARCH=$(uname -m)") {
		t.Errorf("should set BUILD_ARCH inside shell:\n%s", out)
	}
}

func TestEmitCmd_UserNpmCache(t *testing.T) {
	var b strings.Builder
	emitCmd(&b,
		Op{Plugin: "command", PluginInput: map[string]any{"command": "xdg-settings default-browser foo"}, RunAs: "${USER}"},
		"my-layer", testResolvedBox(), false,
	)
	out := b.String()
	if strings.Contains(out, "libdnf5") {
		t.Errorf("non-root cmd should NOT include distro cache:\n%s", out)
	}
	if !strings.Contains(out, "/tmp/npm-cache") {
		t.Errorf("non-root cmd should include npm cache:\n%s", out)
	}
	if !strings.Contains(out, "uid=1000,gid=1000") {
		t.Errorf("npm cache should be UID/GID owned:\n%s", out)
	}
}

// --- emitTasks orchestrator ---

func TestEmitTasks_UserCoalescing(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{BuildDir: dir}
	ops := []Op{
		{Mkdir: "/a", RunAs: "root"},
		{Mkdir: "/b", RunAs: "root"},
		{Mkdir: "/c", RunAs: "root"}, // all root → single USER 0 header, one RUN
	}
	layer := &Candy{Name: "lyr"}
	var b strings.Builder
	_, err := g.emitTasks(&b, layer, testResolvedBox(), ops, dir, ".build/test-img")
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
	ops := []Op{
		{Plugin: "command", PluginInput: map[string]any{"command": "echo rpmfusion-enable"}, RunAs: "root"},
	}
	layer := &Candy{Name: "lyr"}
	var b strings.Builder
	_, err := g.emitTasks(&b, layer, testResolvedBox(), ops, dir, ".build/test-img")
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
	ops := []Op{
		{Mkdir: "/a", RunAs: "root"},
		{Mkdir: "/b", RunAs: "${USER}"},
		{Mkdir: "/c", RunAs: "${USER}"}, // coalesces with previous
	}
	layer := &Candy{Name: "lyr"}
	var b strings.Builder
	_, err := g.emitTasks(&b, layer, testResolvedBox(), ops, dir, ".build/test-img")
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
	ops := []Op{
		{Mkdir: "/a", RunAs: "root"},
		{Copy: "f", To: "/a/f", RunAs: "root"},
		{Mkdir: "/b", RunAs: "root"},
	}
	layer := &Candy{Name: "lyr"}
	var b strings.Builder
	_, err := g.emitTasks(&b, layer, testResolvedBox(), ops, dir, ".build/test-img")
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
	ops := []Op{
		// Copy to /etc/traefik/traefik.yml without declaring /etc/traefik first
		{Copy: "traefik.yml", To: "/etc/traefik/traefik.yml", RunAs: "root"},
	}
	layer := &Candy{Name: "lyr"}
	var b strings.Builder
	_, err := g.emitTasks(&b, layer, testResolvedBox(), ops, dir, ".build/test-img")
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
	ops := []Op{
		{Mkdir: "/etc/foo", RunAs: "root"},
		{Copy: "bar", To: "/etc/foo/bar", RunAs: "root"},
	}
	layer := &Candy{Name: "lyr"}
	var b strings.Builder
	_, err := g.emitTasks(&b, layer, testResolvedBox(), ops, dir, ".build/test-img")
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
	ops := []Op{
		{Write: "/etc/foo.conf", Content: "hello world\n", RunAs: "root"},
	}
	layer := &Candy{Name: "lyr"}
	var b strings.Builder
	buildDir := filepath.Join(dir, "test-img")
	_, err := g.emitTasks(&b, layer, testResolvedBox(), ops, buildDir, ".build/test-img")
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

// --- emitVarsEnv ---

func TestEmitVarsEnv_AlwaysEmitsArch(t *testing.T) {
	var b strings.Builder
	emitVarsEnv(&b, nil)
	out := b.String()
	if !strings.Contains(out, "ARG TARGETARCH") {
		t.Errorf("expected ARG TARGETARCH:\n%s", out)
	}
	if !strings.Contains(out, "ENV ARCH=${TARGETARCH}") {
		t.Errorf("expected ENV ARCH=${TARGETARCH}:\n%s", out)
	}
}

func TestEmitVarsEnv_SortedKeys(t *testing.T) {
	var b strings.Builder
	emitVarsEnv(&b, map[string]string{"ZETA": "z", "ALPHA": "a", "MIDDLE": "m"})
	out := b.String()
	idxA := strings.Index(out, "ENV ALPHA")
	idxM := strings.Index(out, "ENV MIDDLE")
	idxZ := strings.Index(out, "ENV ZETA")
	if idxA >= idxM || idxM >= idxZ {
		t.Errorf("vars should be emitted in sorted order:\n%s", out)
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

func TestCandy_HasInstallFiles_IncludesTasks(t *testing.T) {
	l := &Candy{plan: []Step{{Run: "build", Op: cmdOp("true")}}}
	if !l.HasInstallFiles() {
		t.Error("HasInstallFiles() should be true when HasTasks is true")
	}
}
