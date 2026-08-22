package refs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPickResolvedCommit guards the Bug-2 fix: an annotated tag must resolve to
// the underlying COMMIT (refs/tags/X^{}), never the tag OBJECT (refs/tags/X).
// Returning the tag object made a later `git clone --depth 1 --branch <tag>`
// emit git's "refs/tags/X <sha> is not a commit!" warning.
func TestPickResolvedCommit(t *testing.T) {
	const tagObj = "c85de9810981f6655e8f9a5d2307460c0456d780"
	const commit = "2d731456b0b8cfbe2e19b64de75b4d652d2fc94c"
	cases := []struct {
		name      string
		lines     []string
		ref, want string
	}{
		{"annotated tag prefers the peeled commit, not the tag object",
			[]string{tagObj + "\trefs/tags/v1.0.0", commit + "\trefs/tags/v1.0.0^{}"}, "v1.0.0", commit},
		{"peeled line first still wins",
			[]string{commit + "\trefs/tags/v1.0.0^{}", tagObj + "\trefs/tags/v1.0.0"}, "v1.0.0", commit},
		{"lightweight tag (no peel) returns its direct sha",
			[]string{commit + "\trefs/tags/v1.0.0"}, "v1.0.0", commit},
		{"branch returns the head sha",
			[]string{commit + "\trefs/heads/main"}, "main", commit},
		{"ref absent returns empty",
			[]string{commit + "\trefs/tags/v9.9.9"}, "v1.0.0", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickResolvedCommit(c.lines, c.ref); got != c.want {
				t.Errorf("pickResolvedCommit(%v, %q) = %q, want %q", c.lines, c.ref, got, c.want)
			}
		})
	}
}

func TestParseDefaultBranch(t *testing.T) {
	tests := []struct {
		output string
		want   string
	}{
		{"ref: refs/heads/main\tHEAD\nabc123\tHEAD\n", "main"},
		{"ref: refs/heads/master\tHEAD\ndef456\tHEAD\n", "master"},
		{"ref: refs/heads/develop\tHEAD\n789abc\tHEAD\n", "develop"},
		{"abc123\tHEAD\n", ""}, // no symref line
		{"", ""},               // empty output
	}

	for _, tt := range tests {
		got := parseDefaultBranch(tt.output)
		if got != tt.want {
			t.Errorf("parseDefaultBranch(%q) = %q, want %q", tt.output, got, tt.want)
		}
	}
}

func TestParseTagRefs(t *testing.T) {
	output := `abc123def456	refs/tags/v0.1.0
def456abc789	refs/tags/v0.1.0^{}
111222333444	refs/tags/v1.0.0
555666777888	refs/tags/v1.0.0^{}
aaa111bbb222	refs/tags/v2.0.0
ccc333ddd444	refs/tags/v2.0.0^{}
eee555fff666	refs/tags/not-semver
`
	tags := parseTagRefs(output)
	if len(tags) != 3 {
		t.Fatalf("len(tags) = %d, want 3", len(tags))
	}
	// Should contain v0.1.0, v1.0.0, v2.0.0 (no ^{} or non-v tags)
	want := map[string]bool{"v0.1.0": true, "v1.0.0": true, "v2.0.0": true}
	for _, tag := range tags {
		if !want[tag] {
			t.Errorf("unexpected tag %q", tag)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v2.0.0", -1},
		{"v2.0.0", "v1.0.0", 1},
		{"v1.0.0", "v1.1.0", -1},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.9.0", "v1.10.0", -1},
		{"v0.1.0", "v1.0.0", -1},
	}

	for _, tt := range tests {
		got := CompareSemver(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestIsHex(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"abc123", true},
		{"ABC123", true},
		{"deadbeef", true},
		{"", false},
		{"xyz", false},
		{"abc 123", false},
	}

	for _, tt := range tests {
		got := isHex(tt.s)
		if got != tt.want {
			t.Errorf("isHex(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}

func TestRepoGitURL(t *testing.T) {
	got := RepoGitURL("github.com/opencharly/ml-layers")
	want := "https://github.com/opencharly/ml-layers.git"
	if got != want {
		t.Errorf("RepoGitURL() = %q, want %q", got, want)
	}
}

// TestPopulateSubmodules_NoGitmodulesIsNoOp: a repo declaring no submodules at
// all is a clean no-op — a box/<distro> repo has none and must not error.
//
// Note the deliberately narrowed contract: this used to also assert that a
// .gitmodules declaring only box/arch was a no-op, because just `sdk` was ever
// initialized. Populating EVERY declared submodule is the fix, so that case is
// no longer a no-op and asserting it would pin the bug in place.
func TestPopulateSubmodules_NoGitmodulesIsNoOp(t *testing.T) {
	if err := populateSubmodules(t.TempDir()); err != nil { // no .gitmodules at all
		t.Fatalf("no-.gitmodules dir must be a no-op, got %v", err)
	}
}

// TestPopulateSubmodules_UnreachableSubmoduleFailsLoudly covers the error branch,
// which is a DELIBERATE widening and therefore needs a test rather than silence.
// The old code no-op'd for any repo not declaring `sdk`, so a project with a
// private or dead submodule fetched "fine"; now it errors. That is the intended
// trade — a silently partial cache is the defect this function exists to fix —
// but the failure must name the submodule and the cause, or it just moves the
// mystery. Uses a file:// URL that does not exist, so it fails fast offline.
func TestPopulateSubmodules_UnreachableSubmoduleFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "seed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitmodules"),
		[]byte("[submodule \"ghost\"]\n\tpath = ghost\n\turl = file:///nonexistent/ghost.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The .gitmodules entry alone is NOT enough: `git submodule update --init`
	// walks the INDEX, so a declared-but-unregistered submodule is ignored and the
	// command exits 0. A gitlink (mode 160000) has to exist for the fetch — and
	// therefore the failure — to happen at all.
	reg := exec.Command("git", "update-index", "--add", "--cacheinfo",
		"160000,0000000000000000000000000000000000000001,ghost")
	reg.Dir = dir
	if out, err := reg.CombinedOutput(); err != nil {
		t.Fatalf("registering the gitlink: %v\n%s", err, out)
	}

	err := populateSubmodules(dir)
	if err == nil {
		t.Fatal("an unreachable submodule must fail the clone, not populate a partial cache")
	}
	// The message has to carry enough to act on: which tree, and git's own reason.
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error must name the target dir, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error must name the offending submodule, got: %v", err)
	}
}

// TestRepoCacheFresh_IncompleteExportIsStale is the self-heal gate: a cache whose
// commit matches but whose CONTENT is incomplete must read as STALE, so it is
// re-downloaded rather than served forever.
//
// Without this, the submodule fix would only ever help caches created after it.
// Every cache written by the old sdk-only code holds one of twelve submodules
// AND records the correct commit, so the old commit-only freshness check served
// it as a permanent hit until main happened to advance — and there is no CLI
// verb to invalidate a repo cache entry. This test is what fails if freshness
// regresses to comparing commits alone.
func TestRepoCacheFresh_IncompleteExportIsStale(t *testing.T) {
	const commit = "0123456789012345678901234567890123456789"
	mk := func(t *testing.T, populate bool) string {
		t.Helper()
		dir := t.TempDir()
		cache := filepath.Join(dir, "export")
		if err := os.MkdirAll(filepath.Join(cache, "sdk"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Two declared submodules, mirroring the real shape: sdk populated, spec empty.
		if err := os.WriteFile(filepath.Join(cache, "sdk", "go.mod"), []byte("module x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(cache, "spec"), 0o755); err != nil {
			t.Fatal(err)
		}
		if populate {
			if err := os.WriteFile(filepath.Join(cache, "spec", "go.mod"), []byte("module y\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(cache, ".gitmodules"), []byte(
			"[submodule \"sdk\"]\n\tpath = sdk\n\turl = https://example.invalid/sdk.git\n"+
				"[submodule \"spec\"]\n\tpath = spec\n\turl = https://example.invalid/spec.git\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeRefProvenance(cache, commit); err != nil {
			t.Fatal(err)
		}
		return cache
	}

	// The exact shape the old code left behind: right commit, eleven empty dirs.
	if repoCacheFresh(mk(t, false), commit) {
		t.Error("an export with an EMPTY declared submodule must be stale — otherwise the " +
			"one-of-twelve cache is served forever and the submodule fix never reaches existing users")
	}
	// A complete export at the same commit must still be a cache HIT; over-invalidating
	// would re-clone every repo on every access.
	if !repoCacheFresh(mk(t, true), commit) {
		t.Error("a COMPLETE export at the recorded commit must remain a cache hit")
	}
	// A repo declaring no submodules at all is trivially complete.
	bare := t.TempDir()
	if err := writeRefProvenance(bare, commit); err != nil {
		t.Fatal(err)
	}
	if !repoCacheFresh(bare, commit) {
		t.Error("an export declaring no submodules must remain a cache hit")
	}

	// A .gitmodules entry with NO gitlink materializes NO directory (verified:
	// git creates an empty placeholder only for paths it actually clones). Such
	// an entry is unfetchable — populateSubmodules walks the INDEX and skips it
	// too — so an ABSENT directory must NOT read as incomplete. Conflating absent
	// with empty made the export permanently unfresh, re-cloning the repo on
	// EVERY command forever; this is the assertion that catches that.
	nogl := t.TempDir()
	if err := os.WriteFile(filepath.Join(nogl, ".gitmodules"),
		[]byte("[submodule \"ghost\"]\n\tpath = ghost\n\turl = https://example.invalid/g.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeRefProvenance(nogl, commit); err != nil {
		t.Fatal(err)
	}
	if !repoCacheFresh(nogl, commit) {
		t.Error("a .gitmodules entry with no gitlink (no directory on disk) must NOT make the " +
			"export stale — it is unfetchable, so this would re-clone on every command forever")
	}
}

// TestGitClone_PopulatesAllSubmodules is the end-to-end integration gate: a
// fresh GitClone of the charly repo populates EVERY declared submodule, so a
// --repo cache is a project a user can actually drive with only the binary.
//
// This is the coverage that fails without the fix. `sdk` alone passed before
// and would still pass, so it proves nothing on its own — `spec` is the
// assertion that breaks against the old sdk-only special case, and it is the
// one that matters most: every out-of-process plugin candy carries
// `replace github.com/opencharly/spec => ../../spec`, so without it every such
// build dies on "../../spec/go.mod: no such file" and no out-of-process verb is
// reachable through --repo at all. box/<distro> covers `box build` (main owns
// no boxes), which the old code excluded as "heavy".
//
// Network-gated: skipped offline.
func TestGitClone_PopulatesAllSubmodules(t *testing.T) {
	if testing.Short() {
		t.Skip("network integration test")
	}
	if exec.Command("git", "ls-remote", "https://github.com/opencharly/charly.git", "HEAD").Run() != nil {
		t.Skip("no network / github unreachable")
	}
	dir := filepath.Join(t.TempDir(), "clone")
	if err := GitClone("https://github.com/opencharly/charly.git", "main", "", dir); err != nil {
		t.Fatalf("GitClone: %v", err)
	}
	for _, probe := range []struct{ path, why string }{
		{"sdk/go.mod", "plugin builds fail 'cannot load module ../../sdk'"},
		{"spec/go.mod", "plugin builds fail 'cannot load module ../../spec' — every out-of-process verb unreachable via --repo"},
		{"box/fedora/charly.yml", "`charly --repo … box build` has no box definitions; main owns none"},
		{"plugins/README.md", "the skill corpus is absent"},
	} {
		if _, err := os.Stat(filepath.Join(dir, probe.path)); err != nil {
			t.Errorf("submodule path %s not populated (%s): %v", probe.path, probe.why, err)
		}
	}
}
