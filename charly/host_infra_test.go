package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/vmshared"
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
		k, v, ok := vmshared.SplitOsReleaseLine(tc.line)
		if k != tc.k || v != tc.v || ok != tc.ok {
			t.Errorf("splitOsReleaseLine(%q) = (%q, %q, %v); want (%q, %q, %v)",
				tc.line, k, v, ok, tc.k, tc.v, tc.ok)
		}
	}
}

func TestHostDistroTagsAndFormatHint(t *testing.T) {
	tests := []struct {
		hd      *vmshared.HostDistro
		wantTag string
		wantFmt string
	}{
		{
			hd:      &vmshared.HostDistro{ID: "fedora", VersionID: "43"},
			wantTag: "fedora:43",
			wantFmt: "rpm",
		},
		{
			hd:      &vmshared.HostDistro{ID: "ubuntu", VersionID: "24.04", IDLike: []string{"debian"}},
			wantTag: "ubuntu:24.04",
			wantFmt: "deb",
		},
		{
			hd:      &vmshared.HostDistro{ID: "arch"},
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
		if got := vmshared.ParseGlibcVersion(in); got != want {
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
		if got := vmshared.CompareGlibc(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareGlibc(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// ---------------- install_ledger.go ----------------

func withTempLedger(t *testing.T) *kit.LedgerPaths {
	t.Helper()
	root := t.TempDir()
	return &kit.LedgerPaths{
		Root:     root,
		Deploys:  filepath.Join(root, "deploys"),
		Candies:  filepath.Join(root, "layers"),
		LockFile: filepath.Join(root, ".lock"),
	}
}

func TestLedgerRoundTrip(t *testing.T) {
	paths := withTempLedger(t)
	rec := &kit.DeployRecord{
		DeployID:   "abc123",
		Image:      "fedora-coder",
		Target:     "host",
		Candy:      []string{"ripgrep", "uv"},
		DeployedAt: "2026-04-21T00:00:00Z",
	}
	if err := kit.WriteDeployRecord(paths, rec); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := kit.ReadDeployRecord(paths, "abc123")
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
	if err := kit.AddCandyDeployment(paths, "ripgrep", "deploy-A", nil); err != nil {
		t.Fatal(err)
	}
	if err := kit.AddCandyDeployment(paths, "ripgrep", "deploy-B", nil); err != nil {
		t.Fatal(err)
	}
	rec, _ := kit.ReadCandyRecord(paths, "ripgrep")
	if len(rec.DeployedBy) != 2 {
		t.Errorf("DeployedBy = %v, want 2 entries", rec.DeployedBy)
	}

	// Remove A — ripgrep stays.
	_, shouldRemove, err := kit.RemoveCandyDeployment(paths, "ripgrep", "deploy-A")
	if err != nil {
		t.Fatal(err)
	}
	if shouldRemove {
		t.Errorf("shouldRemove=true after removing one of two deployers")
	}
	rec, _ = kit.ReadCandyRecord(paths, "ripgrep")
	if len(rec.DeployedBy) != 1 || rec.DeployedBy[0] != "deploy-B" {
		t.Errorf("after decrement: %v", rec.DeployedBy)
	}

	// Remove B — ripgrep should fully teardown.
	_, shouldRemove, err = kit.RemoveCandyDeployment(paths, "ripgrep", "deploy-B")
	if err != nil {
		t.Fatal(err)
	}
	if !shouldRemove {
		t.Errorf("shouldRemove=false when DeployedBy drains to empty")
	}
}

func TestLedgerFlock(t *testing.T) {
	paths := withTempLedger(t)
	lock, err := kit.AcquireLedgerLock(paths)
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
	opts := deploykit.BuilderRunOpts{
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
	args := kit.BuildBuilderRunArgs(opts)
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
	out, err := kit.BuilderRun(context.Background(), deploykit.BuilderRunOpts{
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
//
// The env.d rendering / managed-block-body / shell-init-path / marker /
// shell-detection tests that used to live here (TestRenderEnvdBody,
// TestRenderEnvdBodyPathDoubleQuoted, TestManagedBlockBodyGlobUnquoted,
// TestShellInitFilePath, TestShQuoteEnv) tested charly-local functions
// deleted as dead code in the R5 sweep (Cutover B unit 3+4) — the
// sdk/kit/profile.go equivalents they duplicated (RenderEnvdBody/
// ManagedBlockBody/ShellInitFilePath/DetectShellFromPath) carry their own
// coverage in that package. The two surviving tests below now build their
// marker fences via kit.MarkersForTag instead of the deleted local
// markersForTag, and TestRemoveEnvdFile covers the one function this file
// still owns.

// TestRemoveManagedBlockAt proves the LOCAL per-candy teardown strip (the live path
// reverseRemoveManaged takes when runner==nil): a candy's fenced shell-snippet block is
// removed from an rc file the user also owns, leaving the user's own content intact.
func TestRemoveManagedBlockAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".bashrc")
	begin, end := kit.MarkersForTag("mycandy")
	content := "export USER_VAR=1\n" + begin + "\nexport CANDY_VAR=2\n" + end + "\nalias ll='ls -l'\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := kit.RemoveManagedBlockAt(path, "mycandy"); err != nil {
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
	begin, end := kit.MarkersForTag("mycandy")
	content := "export USER_VAR=1\n" + begin + "\nexport CANDY_VAR=2\n" + end + "\nalias ll='ls -l'\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-c", kit.RenderManagedBlockStrip(path, "mycandy")).CombinedOutput(); err != nil {
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

// TestRemoveEnvdFile proves the ONE function shell_profile.go still owns:
// removal is silent-success both when the file exists and when it's already
// gone (double-remove). The file is created directly via kit.EnvdFilePath +
// kit.RenderEnvdBody — the same primitives the live kit.WalkPlans write path
// uses — rather than through the (now-deleted) charly-local WriteEnvdFile.
func TestRemoveEnvdFile(t *testing.T) {
	home := t.TempDir()
	path := kit.EnvdFilePath(home, "pre-commit")
	if err := os.MkdirAll(kit.EnvdDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	body := kit.RenderEnvdBody("pre-commit", map[string]string{"K": "v"}, []string{"/bin"})
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
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

// TestBuildBuilderRunArgsRunAsRoot asserts the RunAsRoot path emits
// `--user 0:0`. the local deploy target.execBuilder always sets RunAsRoot=true
// because rootless podman maps in-container uid 0 to the operator's host
// uid; bind-mounts of $HOME/.cargo / $HOME/.npm-global / etc. are then
// writable. Without this flag the in-container user is mapped to a
// subordinate uid that doesn't match the bind-mount owner and writes
// fail with EACCES.
func TestBuildBuilderRunArgsRunAsRoot(t *testing.T) {
	opts := deploykit.BuilderRunOpts{
		BuilderImage: "arch-builder:latest",
		CandyDir:     "/home/user/layers/pre-commit",
		HostHome:     "/home/user",
		RunAsRoot:    true,
	}
	args := kit.BuildBuilderRunArgs(opts)
	full := strings.Join(args, " ")
	if !strings.Contains(full, "--user 0:0") {
		t.Errorf("RunAsRoot did not emit --user 0:0; got: %s", full)
	}
}
