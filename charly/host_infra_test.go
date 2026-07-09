package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the four Task-9 host-infra files.

// ---------------- hostdistro.go ----------------

func TestDetectHostDistroFedora43(t *testing.T) {
	// Verify parsing against a realistic os-release body — we synthesize
	// a temp file and exercise the line-parsing primitive.
	tests := []struct {
		line string
		k, v string
		ok   bool
	}{
		{`ID=fedora`, "ID", "fedora", true},
		{`VERSION_ID=43`, "VERSION_ID", "43", true},
		{`ID_LIKE="debian ubuntu"`, "ID_LIKE", "debian ubuntu", true},
		{`NAME='Fedora Linux'`, "NAME", "Fedora Linux", true},
		{`# comment`, "", "", false},
		{``, "", "", false},
	}
	for _, tc := range tests {
		k, v, ok := splitOsReleaseLine(tc.line)
		if k != tc.k || v != tc.v || ok != tc.ok {
			t.Errorf("splitOsReleaseLine(%q) = (%q, %q, %v); want (%q, %q, %v)",
				tc.line, k, v, ok, tc.k, tc.v, tc.ok)
		}
	}
}

func TestHostDistroTagsAndFormatHint(t *testing.T) {
	tests := []struct {
		hd      *HostDistro
		wantTag string
		wantFmt string
	}{
		{
			hd:      &HostDistro{ID: "fedora", VersionID: "43"},
			wantTag: "fedora:43",
			wantFmt: "rpm",
		},
		{
			hd:      &HostDistro{ID: "ubuntu", VersionID: "24.04", IDLike: []string{"debian"}},
			wantTag: "ubuntu:24.04",
			wantFmt: "deb",
		},
		{
			hd:      &HostDistro{ID: "arch"},
			wantTag: "arch",
			wantFmt: "pac",
		},
	}
	for _, tc := range tests {
		tc.hd.PopulateTags()
		if got := tc.hd.PrimaryTag(); got != tc.wantTag {
			t.Errorf("PrimaryTag() = %q, want %q", got, tc.wantTag)
		}
		if got := tc.hd.FormatHint(); got != tc.wantFmt {
			t.Errorf("FormatHint() = %q, want %q", got, tc.wantFmt)
		}
	}
}

func TestParseGlibcVersion(t *testing.T) {
	tests := map[string]string{
		"ldd (GNU libc) 2.39\n":                     "2.39",
		"ldd (Ubuntu GLIBC 2.35-0ubuntu3.8) 2.35\n": "2.35",
		"ldd (GNU libc) 2.38.0\n":                   "2.38",
		"something unexpected\n":                    "",
		"":                                          "",
	}
	for in, want := range tests {
		if got := parseGlibcVersion(in); got != want {
			t.Errorf("parseGlibcVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompareGlibc(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"2.39", "2.35", 1},
		{"2.35", "2.39", -1},
		{"2.35", "2.35", 0},
		{"2.39.0", "2.39.1", 0},
		{"2.40", "2.9", 1},
		{"", "2.39", 0}, // unknown compares equal
		{"2.39", "", 0},
	}
	for _, tc := range tests {
		if got := CompareGlibc(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareGlibc(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// ---------------- install_ledger.go ----------------

func withTempLedger(t *testing.T) *LedgerPaths {
	t.Helper()
	root := t.TempDir()
	return &LedgerPaths{
		Root:     root,
		Deploys:  filepath.Join(root, "deploys"),
		Candies:  filepath.Join(root, "layers"),
		LockFile: filepath.Join(root, ".lock"),
	}
}

func TestLedgerRoundTrip(t *testing.T) {
	paths := withTempLedger(t)
	rec := &DeployRecord{
		DeployID:   "abc123",
		Image:      "fedora-coder",
		Target:     "host",
		Candy:      []string{"ripgrep", "uv"},
		DeployedAt: "2026-04-21T00:00:00Z",
	}
	if err := WriteDeployRecord(paths, rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadDeployRecord(paths, "abc123")
	if err != nil || got == nil {
		t.Fatalf("read: %v / %+v", err, got)
	}
	if got.Image != "fedora-coder" || len(got.Candy) != 2 {
		t.Errorf("round-trip broken: %+v", got)
	}
}

func TestLedgerRefcount(t *testing.T) {
	paths := withTempLedger(t)
	// Deploy A and B both include ripgrep.
	if err := AddCandyDeployment(paths, "ripgrep", "deploy-A", nil); err != nil {
		t.Fatal(err)
	}
	if err := AddCandyDeployment(paths, "ripgrep", "deploy-B", nil); err != nil {
		t.Fatal(err)
	}
	rec, _ := ReadCandyRecord(paths, "ripgrep")
	if len(rec.DeployedBy) != 2 {
		t.Errorf("DeployedBy = %v, want 2 entries", rec.DeployedBy)
	}

	// Remove A — ripgrep stays.
	_, shouldRemove, err := RemoveCandyDeployment(paths, "ripgrep", "deploy-A")
	if err != nil {
		t.Fatal(err)
	}
	if shouldRemove {
		t.Errorf("shouldRemove=true after removing one of two deployers")
	}
	rec, _ = ReadCandyRecord(paths, "ripgrep")
	if len(rec.DeployedBy) != 1 || rec.DeployedBy[0] != "deploy-B" {
		t.Errorf("after decrement: %v", rec.DeployedBy)
	}

	// Remove B — ripgrep should fully teardown.
	_, shouldRemove, err = RemoveCandyDeployment(paths, "ripgrep", "deploy-B")
	if err != nil {
		t.Fatal(err)
	}
	if !shouldRemove {
		t.Errorf("shouldRemove=false when DeployedBy drains to empty")
	}
}

func TestLedgerFlock(t *testing.T) {
	paths := withTempLedger(t)
	lock, err := AcquireLedgerLock(paths)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Can't easily test contention without a second process — at least
	// verify release succeeds and the lock file exists.
	if _, err := os.Stat(paths.LockFile); err != nil {
		t.Errorf("lock file not created: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("release: %v", err)
	}
}

// ---------------- builder_run.go ----------------

func TestBuildBuilderRunArgs(t *testing.T) {
	opts := BuilderRunOpts{
		BuilderImage: "fedora-builder:latest",
		CandyDir:     "/home/user/layers/pre-commit",
		HostHome:     "/home/user",
		BindMounts: map[string]string{
			"/home/user/.pixi": "/home/user/.pixi",
		},
		Env: map[string]string{
			"PIXI_CACHE_DIR": "/home/user/.cache/charly/pixi",
		},
	}
	args := BuildBuilderRunArgs(opts)
	want := []string{
		"run", "--rm",
		"--pull=never", // EnsureImagePresent has already handled the pull/build; suppress podman's auto-pull.
		"--user",       // we don't check the exact uid because it varies
	}
	if len(args) < len(want) {
		t.Fatalf("args too short: %v", args)
	}
	for i, w := range want {
		if args[i] != w {
			t.Errorf("args[%d] = %q, want %q (full: %v)", i, args[i], w, args)
		}
	}
	// Verify critical pieces are present.
	fullCmd := strings.Join(args, " ")
	mustContain := []string{
		"fedora-builder:latest",
		"-v /home/user/.pixi:/home/user/.pixi:rw",
		"-v /home/user/layers/pre-commit:/work:ro",
		"-e HOME=/home/user",
		"-e PIXI_CACHE_DIR=/home/user/.cache/charly/pixi",
		"-w /work",
		"bash -s",
	}
	for _, m := range mustContain {
		if !strings.Contains(fullCmd, m) {
			t.Errorf("missing %q in args: %s", m, fullCmd)
		}
	}
}

func TestBuilderRunDryRun(t *testing.T) {
	// DryRun should return nil, nil without actually exec'ing.
	out, err := BuilderRun(context.Background(), BuilderRunOpts{
		BuilderImage: "fedora-builder",
		DryRun:       true,
		ScriptBody:   "echo hi",
	})
	if err != nil {
		t.Errorf("dry-run should not error: %v", err)
	}
	if out != nil {
		t.Errorf("dry-run should return nil output; got %q", out)
	}
}

// ---------------- shell_profile.go ----------------

func TestRenderEnvdBody(t *testing.T) {
	body := renderEnvdBody("pre-commit",
		map[string]string{
			"PIXI_CACHE_DIR": "/home/u/.cache/pixi",
			"FOO":            "bar baz",
		},
		[]string{"/home/u/.pixi/bin", "/home/u/.local/bin"},
	)
	// Deterministic ordering (sorted keys).
	lines := strings.Split(body, "\n")
	// Find the FOO and PIXI_CACHE_DIR lines to check ordering.
	var fooIdx, pixiIdx = -1, -1
	for i, l := range lines {
		if strings.HasPrefix(l, "export FOO=") {
			fooIdx = i
		}
		if strings.HasPrefix(l, "export PIXI_CACHE_DIR=") {
			pixiIdx = i
		}
	}
	if fooIdx == -1 || pixiIdx == -1 {
		t.Fatalf("missing env lines; body:\n%s", body)
	}
	if fooIdx > pixiIdx {
		t.Errorf("sort order broken: FOO at %d, PIXI_CACHE_DIR at %d", fooIdx, pixiIdx)
	}
	if !strings.Contains(body, "export PATH=") {
		t.Errorf("missing PATH line; body:\n%s", body)
	}
	if !strings.Contains(body, "/home/u/.pixi/bin") {
		t.Errorf("missing pixi bin in PATH; body:\n%s", body)
	}
}

// TestRemoveManagedBlockAt proves the LOCAL per-candy teardown strip (the live path
// reverseRemoveManaged takes when runner==nil): a candy's fenced shell-snippet block is
// removed from an rc file the user also owns, leaving the user's own content intact.
func TestRemoveManagedBlockAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".bashrc")
	begin, end := markersForTag("mycandy")
	content := "export USER_VAR=1\n" + begin + "\nexport CANDY_VAR=2\n" + end + "\nalias ll='ls -l'\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveManagedBlockAt(path, "mycandy"); err != nil {
		t.Fatalf("RemoveManagedBlockAt: %v", err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "CANDY_VAR") || strings.Contains(string(got), begin) {
		t.Errorf("per-candy managed block not stripped:\n%s", got)
	}
	if !strings.Contains(string(got), "USER_VAR") || !strings.Contains(string(got), "alias ll") {
		t.Errorf("user content lost during strip:\n%s", got)
	}
}

// TestRenderManagedBlockStrip proves the REMOTE per-candy teardown strip (the live path
// reverseRemoveManaged takes when runner!=nil): the rendered POSIX-sh script, run through
// a real shell, strips exactly the candy's fence pair in place and preserves the rest.
func TestRenderManagedBlockStrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".bashrc")
	begin, end := markersForTag("mycandy")
	content := "export USER_VAR=1\n" + begin + "\nexport CANDY_VAR=2\n" + end + "\nalias ll='ls -l'\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-c", renderManagedBlockStrip(path, "mycandy")).CombinedOutput(); err != nil {
		t.Fatalf("strip script failed: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "CANDY_VAR") || strings.Contains(string(got), begin) {
		t.Errorf("remote strip left the managed block:\n%s", got)
	}
	if !strings.Contains(string(got), "USER_VAR") || !strings.Contains(string(got), "alias ll") {
		t.Errorf("remote strip lost user content:\n%s", got)
	}
}

// TestRenderEnvdBodyPathDoubleQuoted asserts the PATH export in an
// env.d file uses double quotes so $PATH EXPANDS at sourcing time.
// A previous version single-quoted the value, which left the PATH
// literally set to "/some/dir:$PATH" — and each subsequent candy's
// env.d then clobbered it with another literal "/other/dir:$PATH",
// losing every previous candy's PATH entries (npm-global, cargo,
// pixi, ...). The result was that npm-installed binaries weren't
// on PATH after a target:local deploy unless the user manually
// fixed up their shell.
func TestRenderEnvdBodyPathDoubleQuoted(t *testing.T) {
	body := renderEnvdBody("nodejs", nil, []string{"/home/u/.npm-global/bin"})
	if !strings.Contains(body, `export PATH="/home/u/.npm-global/bin:$PATH"`) {
		t.Errorf("PATH export not double-quoted with $PATH expansion; got:\n%s", body)
	}
	// Negative: must NOT contain the buggy single-quoted form.
	if strings.Contains(body, `export PATH='/home/u/.npm-global/bin:$PATH'`) {
		t.Errorf("PATH export re-introduced the single-quote bug:\n%s", body)
	}
}

// TestManagedBlockBodyGlobUnquoted asserts the bash/zsh managed-block
// snippet leaves the *.env glob UNQUOTED so the shell expands it. A
// previous version wrapped the path in double quotes which made the
// loop iterate once over the literal pattern, the `[ -r ]` test
// fail, and no env files get sourced — leaving deployed candies'
// PATH / NPM_CONFIG_PREFIX / etc. unset until the user manually
// sourced ~/.config/opencharly/env.d/*.env.
func TestManagedBlockBodyGlobUnquoted(t *testing.T) {
	home := "/home/example"
	body := ManagedBlockBody(ShellBash, home)
	// The literal that used to be present and broke globbing:
	if strings.Contains(body, `"/home/example/.config/opencharly/env.d/*.env"`) {
		t.Errorf("managed block re-introduced the quoted-glob bug:\n%s", body)
	}
	// Unquoted path with the glob present:
	if !strings.Contains(body, `for f in /home/example/.config/opencharly/env.d/*.env`) {
		t.Errorf("managed block missing unquoted glob; got:\n%s", body)
	}
}

func TestShellInitFilePath(t *testing.T) {
	home := "/home/user"
	tests := map[ShellKind]string{
		// bash → ~/.bashrc: a bash login shell prefers ~/.bash_profile (which
		// sources ~/.bashrc) over ~/.profile, so the env.d block must land in
		// ~/.bashrc to actually load in the user's terminal.
		ShellBash: "/home/user/.bashrc",
		ShellZsh:  "/home/user/.zshenv",
		ShellFish: "/home/user/.config/fish/conf.d/opencharly.fish",
	}
	for kind, want := range tests {
		if got := ShellInitFilePath(kind, home); got != want {
			t.Errorf("%v → %q, want %q", kind, got, want)
		}
	}
}

func TestWriteAndRemoveEnvdFile(t *testing.T) {
	home := t.TempDir()
	path, err := WriteEnvdFile(home, "pre-commit", map[string]string{"K": "v"}, []string{"/bin"})
	if err != nil {
		t.Fatalf("WriteEnvdFile: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
	if err := RemoveEnvdFile(home, "pre-commit"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after remove: %v", err)
	}
	// Remove again — should not error.
	if err := RemoveEnvdFile(home, "pre-commit"); err != nil {
		t.Errorf("double-remove errored: %v", err)
	}
}

func TestShQuoteEnv(t *testing.T) {
	tests := map[string]string{
		"simple":         "simple",
		"":               "''",
		"with spaces":    "'with spaces'",
		"has'quote":      `'has'\''quote'`,
		"safe-chars_1.2": "safe-chars_1.2",
		"$VAR":           `'$VAR'`,
	}
	for in, want := range tests {
		if got := shQuoteEnv(in); got != want {
			t.Errorf("shQuoteEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBuildBuilderRunArgsRunAsRoot asserts the RunAsRoot path emits
// `--user 0:0`. the local deploy target.execBuilder always sets RunAsRoot=true
// because rootless podman maps in-container uid 0 to the operator's host
// uid; bind-mounts of $HOME/.cargo / $HOME/.npm-global / etc. are then
// writable. Without this flag the in-container user is mapped to a
// subordinate uid that doesn't match the bind-mount owner and writes
// fail with EACCES.
func TestBuildBuilderRunArgsRunAsRoot(t *testing.T) {
	opts := BuilderRunOpts{
		BuilderImage: "arch-builder:latest",
		CandyDir:     "/home/user/layers/pre-commit",
		HostHome:     "/home/user",
		RunAsRoot:    true,
	}
	args := BuildBuilderRunArgs(opts)
	full := strings.Join(args, " ")
	if !strings.Contains(full, "--user 0:0") {
		t.Errorf("RunAsRoot did not emit --user 0:0; got: %s", full)
	}
}
