package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// stubRefsDownloader counts Download invocations and returns a canned path,
// so EnsureRepoDownloaded's cache-hit-vs-delegate decision is testable offline.
type stubRefsDownloader struct {
	calls int
	path  string
}

func (s *stubRefsDownloader) Download(repoPath, version string) (string, error) {
	s.calls++
	return s.path, nil
}

// TestEnsureRepoDownloaded_MutableRefAlwaysDelegates pins the @main staleness
// fix (the pre-#146 protocol skew): a cached MUTABLE ref (a branch such as
// main) must ALWAYS delegate to the downloader — which re-resolves the ref's
// current commit and refreshes a stale export (kit.DownloadRepo's provenance
// check) — while an immutable tag keeps the offline cache-hit fast path.
func TestEnsureRepoDownloaded_MutableRefAlwaysDelegates(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("CHARLY_REPO_CACHE", cacheRoot)

	// A head-schema charly.yml so cacheBehindHead=false (no migration path).
	head := fmt.Sprintf("version: %v\n", LatestSchemaVersion())
	seed := func(version string) string {
		dir := filepath.Join(cacheRoot, "github.com", "foo", "bar@"+version)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(head), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	mutableCache := seed("main")
	immutableCache := seed("v1.0.0")

	stub := &stubRefsDownloader{path: filepath.Join(cacheRoot, "downloaded")}
	prev := activeRefsDownloader
	activeRefsDownloader = stub
	defer func() { activeRefsDownloader = prev }()

	got, err := EnsureRepoDownloaded("github.com/foo/bar", "main")
	if err != nil {
		t.Fatalf("mutable ref: %v", err)
	}
	if stub.calls != 1 || got != stub.path {
		t.Fatalf("mutable ref must delegate to the downloader even when cached: calls=%d path=%q (cache %q)",
			stub.calls, got, mutableCache)
	}

	got, err = EnsureRepoDownloaded("github.com/foo/bar", "v1.0.0")
	if err != nil {
		t.Fatalf("immutable tag: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("immutable tag must keep the offline cache hit (no download): calls=%d", stub.calls)
	}
	if got != immutableCache {
		t.Fatalf("immutable tag must return the cache path %q, got %q", immutableCache, got)
	}
}

// Guard the kit classifier contract the core decision relies on (the full
// matrix lives in sdk/kit's TestIsMutableRef).
func TestIsMutableRefCoreContract(t *testing.T) {
	if !kit.IsMutableRef("main") || !kit.IsMutableRef("") {
		t.Fatal("branches and the unversioned default branch are mutable")
	}
	if kit.IsMutableRef("v2026.201.0706") || kit.IsMutableRef("2d731456b0b8cfbe2e19b64de75b4d652d2fc94c") {
		t.Fatal("tags and full SHAs are immutable")
	}
}
