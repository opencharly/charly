package refs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestIsMutableRef pins the mutable-vs-immutable ref classifier: branches and
// the empty (default-branch) version are mutable; v-tags and full SHAs are
// immutable.
func TestIsMutableRef(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"", true},
		{"main", true},
		{"master", true},
		{"feat/agent-config-drift-repair", true},
		{"v1.0.0", false},
		{"v2026.201.0706", false},
		{"v0.2026201.603", false},
		{"2d731456b0b8cfbe2e19b64de75b4d652d2fc94c", false},
		{"2d73145", true}, // a short SHA is not a provenance-stable coordinate here
	}
	for _, c := range cases {
		if got := IsMutableRef(c.version); got != c.want {
			t.Errorf("IsMutableRef(%q) = %v, want %v", c.version, got, c.want)
		}
	}
}

// TestRepoCacheFresh pins the provenance contract: only a complete export whose
// sidecar names exactly the resolved commit is fresh; a missing export, a
// missing sidecar (pre-contract cache), a moved ref, or an empty commit are
// all stale.
func TestRepoCacheFresh(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "repo@main")
	commit := "2d731456b0b8cfbe2e19b64de75b4d652d2fc94c"

	if repoCacheFresh(cachePath, commit) {
		t.Fatal("missing export must be stale")
	}
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if repoCacheFresh(cachePath, commit) {
		t.Fatal("export without provenance sidecar (pre-contract cache) must be stale")
	}
	if err := writeRefProvenance(cachePath, "aaa111bbb222aaa111bbb222aaa111bbb222aaa111"); err != nil {
		t.Fatal(err)
	}
	if repoCacheFresh(cachePath, commit) {
		t.Fatal("sidecar naming a different commit (moved ref) must be stale")
	}
	if err := writeRefProvenance(cachePath, commit); err != nil {
		t.Fatal(err)
	}
	if !repoCacheFresh(cachePath, commit) {
		t.Fatal("export with matching provenance must be fresh")
	}
	if repoCacheFresh(cachePath, "") {
		t.Fatal("empty commit is never fresh")
	}
}

// TestDownloadRepoFrom_RefreshesMovedBranch is the end-to-end freshness gate
// against a LOCAL file:// remote (no network): a branch that advances upstream
// must be re-downloaded; an unmoved ref must reuse the cache. This is the
// pre-#146 @main skew regression test — a stale default-branch export must
// never silently serve old content.
func TestDownloadRepoFrom_RefreshesMovedBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	t.Setenv("CHARLY_REPO_CACHE", filepath.Join(root, "cache"))

	git := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	// Local bare remote + one commit on main.
	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	git(root, "init", "--bare", "-q", "-b", "main", remote)
	git(root, "init", "-q", "-b", "main", work)
	if err := os.WriteFile(filepath.Join(work, "marker.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(work, "add", "marker.txt")
	git(work, "commit", "-q", "-m", "v1")
	git(work, "remote", "add", "origin", remote)
	git(work, "push", "-q", "origin", "main")
	remoteURL := "file://" + remote

	first, err := downloadRepoFrom(remoteURL, "local/test", "main")
	if err != nil {
		t.Fatalf("initial download: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(first, "marker.txt")); err != nil || string(got) != "v1\n" {
		t.Fatalf("initial content = %q, %v — want v1", got, err)
	}
	prov1, err := os.ReadFile(first + ".ref")
	if err != nil {
		t.Fatalf("provenance sidecar missing after download: %v", err)
	}

	// Unmoved ref → cache hit: the export must NOT be replaced (provenance
	// sidecar content identical and no fresh clone — marker file mtime proves
	// no re-clone, so compare content + that the same path is returned).
	second, err := downloadRepoFrom(remoteURL, "local/test", "main")
	if err != nil {
		t.Fatalf("second download: %v", err)
	}
	if second != first {
		t.Fatalf("cache path changed between identical resolutions: %q vs %q", second, first)
	}
	if got, _ := os.ReadFile(second + ".ref"); string(got) != string(prov1) {
		t.Fatal("provenance changed on an unmoved ref")
	}

	// Advance main upstream → the cache must refresh and serve the NEW content.
	if err := os.WriteFile(filepath.Join(work, "marker.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(work, "add", "marker.txt")
	git(work, "commit", "-q", "-m", "v2")
	git(work, "push", "-q", "origin", "main")

	third, err := downloadRepoFrom(remoteURL, "local/test", "main")
	if err != nil {
		t.Fatalf("download after branch moved: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(third, "marker.txt")); err != nil || string(got) != "v2\n" {
		t.Fatalf("post-move content = %q, %v — want v2 (stale @main-class regression)", got, err)
	}
	prov3, err := os.ReadFile(third + ".ref")
	if err != nil {
		t.Fatalf("provenance sidecar missing after refresh: %v", err)
	}
	if string(prov3) == string(prov1) {
		t.Fatal("provenance still names the pre-move commit after refresh")
	}

	// The refresh leaves no swap residue.
	for _, leftover := range []string{third + ".download", third + ".gc"} {
		if _, err := os.Stat(leftover); !os.IsNotExist(err) {
			t.Fatalf("swap residue %s must not survive the refresh", leftover)
		}
	}
}
