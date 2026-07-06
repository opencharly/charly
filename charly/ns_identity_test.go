package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNsRepoIdentity covers the repo-identity helper that drives the
// import-namespace cycle-break.
func TestNsRepoIdentity(t *testing.T) {
	cases := []struct{ ref, want string }{
		{"@github.com/o/r:v1.2.3", "github.com/o/r"},
		{"@github.com/o/r/candy/x:v1.2.3", "github.com/o/r"},
		{"@github.com/opencharly/charly:v2026.157.0650", "github.com/opencharly/charly"},
	}
	for _, c := range cases {
		if got := nsRepoIdentity(c.ref, "/base"); got != c.want {
			t.Errorf("nsRepoIdentity(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
	// A local path with no git origin yields "" (graceful degrade).
	if got := nsRepoIdentity("./sub", t.TempDir()); got != "" {
		t.Errorf("nsRepoIdentity(local, no-git) = %q, want \"\"", got)
	}
}

func TestNormalizeGitRemoteURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git@github.com:opencharly/charly.git", "github.com/opencharly/charly"},
		{"https://github.com/opencharly/charly.git", "github.com/opencharly/charly"},
		{"https://github.com/opencharly/charly", "github.com/opencharly/charly"},
		{"ssh://git@github.com/opencharly/charly.git", "github.com/opencharly/charly"},
		{"git://github.com/o/r.git", "github.com/o/r"},
		{"github.com/opencharly/charly", "github.com/opencharly/charly"},
	}
	for _, c := range cases {
		if got := normalizeGitRemoteURL(c.in); got != c.want {
			t.Errorf("normalizeGitRemoteURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeRepoIdentity(t *testing.T) {
	cases := []struct{ in, want string }{
		{"github.com/opencharly/charly", "github.com/opencharly/charly"},
		{"git@github.com:opencharly/charly.git", "github.com/opencharly/charly"},
		{"opencharly/charly", "github.com/opencharly/charly"}, // bare owner/repo → github.com
	}
	for _, c := range cases {
		if got := normalizeRepoIdentity(c.in); got != c.want {
			t.Errorf("normalizeRepoIdentity(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestImportNamespace_DivergentVersionMutualCycle reproduces the production
// failure mode (the main<->cachyos mutual import where the loop's pins diverge)
// and asserts the repo-identity cycle-break resolves it.
//
// Topology (mirrors local-main → cachyos@157.1600 → charly@157.0650 →
// cachyos@146.0754-OLD-SCHEMA):
//
//	local root (repo: github.com/o/root) → imports child@vA
//	child@vA                              → imports root@vB   (back-ref, DIVERGENT version)
//	root@vB                               → imports child@vC  (DIVERGENT child version)
//	child@vC                              → has a pre-migration `discover:` MAP that the
//	                                        current binary CANNOT decode (the real fatal shape)
//
// Without the fix, child@vA's back-ref to root@vB is fetched and recursed into,
// reaching the broken child@vC → fatal unmarshal. With the identity cycle-break,
// child@vA's `up:` resolves to the LOCAL root (root's pins win), so root@vB and
// the broken child@vC are NEVER loaded.
func TestImportNamespace_DivergentVersionMutualCycle(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("CHARLY_REPO_CACHE", cache)

	seed := func(repoAtVer, body string) {
		dir := filepath.Join(cache, repoAtVer)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// child@vA imports the root back at a DIVERGENT version (vB).
	seed("github.com/o/child@vA", `version: 2026.186.2323
import:
  - up: '@github.com/o/root:vB'
widget:
  candy:
    base: quay.io/fedora/fedora:43
    build: [rpm]
    distro: [fedora]
`)
	// root@vB pins a DIVERGENT child (vC) — must never be loaded under the fix.
	seed("github.com/o/root@vB", `version: 2026.186.2323
import:
  - child: '@github.com/o/child:vC'
`)
	// child@vC carries the pre-migration `discover:` MAP — decoding it is the
	// fatal error the fix must avoid ever reaching.
	seed("github.com/o/child@vC", `version: 2026.144.1443
discover:
  layer:
    - path: layers
`)

	// Local root declares its identity explicitly and imports child@vA.
	root := t.TempDir()
	writeFixture(t, root, "charly.yml", `version: 2026.186.2323
repo: github.com/o/root
import:
  - child: '@github.com/o/child:vA'
app:
  candy:
    base: child.widget
    build: [rpm]
    distro: [fedora]
`)

	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified (divergent mutual import must cycle-break by repo identity, never decoding child@vC): %v", err)
	}
	child := uf.Namespaces["child"]
	if child == nil {
		t.Fatal("child namespace not mounted")
	}
	// child@vA's back-ref (`up: root@vB`) must resolve to the LOCAL root node
	// (identity cycle-break) — the root's pins win.
	if up := child.Namespaces["up"]; up != uf {
		t.Fatalf("child.up did not resolve to the local root node (cycle-break by repo identity failed); got %p want %p", up, uf)
	}
}
