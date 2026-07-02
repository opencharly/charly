package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestIsFreshnessSafeVerb locks in which verb paths are read-only
// diagnostics that bypass the freshness check. Adding a new safe verb
// is a deliberate decision — verbs that build, deploy, or write must
// always go through the check.
func TestIsFreshnessSafeVerb(t *testing.T) {
	cases := []struct {
		verb string
		safe bool
	}{
		// Read-only diagnostics — must be safe so `charly version` etc.
		// remain runnable when the user is investigating a stale-binary
		// error message.
		{"version", true},
		{"help", true},
		{"status", true},
		{"box inspect foo", true}, // sub-verbs match by prefix
		{"box list boxes", true},
		{"box validate", true},
		{"bundle show foo", true},
		{"secrets list", true},
		{"settings show", true},

		// Heavy verbs — must NOT be safe; freshness check applies.
		{"box build foo", false},
		{"box generate", false},
		{"bundle add foo bar", false},
		{"rebuild versa", false},
		{"start", false},
		{"update", false},
		{"check box foo", false},
		{"check live foo", false},
		{"vm create arch", false},
		{"config foo", false},
		{"secrets set charly/api-key foo bar", false},

		// Edge case: empty verb (shouldn't happen in practice but the
		// helper must be defensive).
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			got := isFreshnessSafeVerb(tc.verb)
			if got != tc.safe {
				t.Errorf("isFreshnessSafeVerb(%q) = %v, want %v", tc.verb, got, tc.safe)
			}
		})
	}
}

// TestFindCharlySourceRoot_DualMarker verifies the dual-marker requirement:
// the function only returns a directory when BOTH charly/main.go AND
// charly.yml are present. A directory with only one of the two markers
// is NOT identified as an opencharly source tree.
func TestFindCharlySourceRoot_DualMarker(t *testing.T) {
	root := t.TempDir()

	// Layout:
	//   <root>/                    ← only charly.yml; not a source tree
	//     charly.yml
	//   <root>/proj/               ← both markers; IS the source tree
	//     charly.yml
	//     charly/main.go
	//   <root>/proj/sub/deeper/    ← deep cwd inside the source tree
	mustWrite(t, filepath.Join(root, "charly.yml"), "")
	proj := filepath.Join(root, "proj")
	mustMkdir(t, filepath.Join(proj, "charly"))
	mustWrite(t, filepath.Join(proj, "charly.yml"), "")
	mustWrite(t, filepath.Join(proj, "charly", "main.go"), "package main")
	deep := filepath.Join(proj, "sub", "deeper")
	mustMkdir(t, deep)

	cases := []struct {
		name string
		from string
		want string
	}{
		{"single-marker root returns empty", root, ""},
		{"dual-marker dir returns itself", proj, proj},
		{"deep cwd walks up to source root", deep, proj},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findCharlySourceRoot(tc.from)
			if got != tc.want {
				t.Errorf("findCharlySourceRoot(%q) = %q, want %q", tc.from, got, tc.want)
			}
		})
	}
}

// TestFindCharlySourceRoot_NoMarker confirms that a cwd outside any source
// tree returns empty (skipping the check entirely — the binary may
// validly run against arbitrary projects).
func TestFindCharlySourceRoot_NoMarker(t *testing.T) {
	root := t.TempDir()
	got := findCharlySourceRoot(root)
	if got != "" {
		t.Errorf("findCharlySourceRoot(%q) = %q, want empty", root, got)
	}
}

// TestNewestGoFile_PicksLatest validates the mtime comparison: when a
// directory contains multiple .go files with different mtimes, the
// helper returns the path with the latest mtime.
func TestNewestGoFile_PicksLatest(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "older.go")
	newer := filepath.Join(dir, "newer.go")
	other := filepath.Join(dir, "not_go.txt")
	mustWrite(t, older, "package x")
	mustWrite(t, newer, "package x")
	mustWrite(t, other, "irrelevant")

	pastTime := time.Now().Add(-1 * time.Hour)
	futureTime := time.Now().Add(1 * time.Hour)
	mustChtime(t, older, pastTime)
	mustChtime(t, newer, futureTime)
	// non-.go file gets a future mtime to confirm the helper ignores it
	mustChtime(t, other, futureTime.Add(1*time.Hour))

	gotPath, gotMtime := newestGoFile(dir)
	if gotPath != newer {
		t.Errorf("newestGoFile path = %q, want %q", gotPath, newer)
	}
	// Allow 1-second slack for FS timestamp rounding
	if gotMtime.Before(futureTime.Add(-time.Second)) || gotMtime.After(futureTime.Add(time.Second)) {
		t.Errorf("newestGoFile mtime = %v, want approximately %v", gotMtime, futureTime)
	}
}

// TestNewestGoFile_SkipsVendor confirms vendor / node_modules / .git
// directories are NOT walked. A .go file in node_modules with a future
// mtime must be ignored in favor of the older one in the project root.
func TestNewestGoFile_SkipsVendor(t *testing.T) {
	dir := t.TempDir()

	root := filepath.Join(dir, "root.go")
	mustWrite(t, root, "package x")
	mustChtime(t, root, time.Now().Add(-1*time.Hour))

	for _, sub := range []string{"vendor", "node_modules", ".git"} {
		subdir := filepath.Join(dir, sub)
		mustMkdir(t, subdir)
		f := filepath.Join(subdir, "shouldnt_count.go")
		mustWrite(t, f, "package x")
		mustChtime(t, f, time.Now().Add(1*time.Hour))
	}

	gotPath, _ := newestGoFile(dir)
	if gotPath != root {
		t.Errorf("newestGoFile path = %q, want %q (vendor/node_modules/.git must be skipped)", gotPath, root)
	}
}

// TestNewestGoFile_EmptyDir returns ("", zero-time) without erroring
// when the directory has no .go files.
func TestNewestGoFile_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	gotPath, gotMtime := newestGoFile(dir)
	if gotPath != "" {
		t.Errorf("newestGoFile path = %q, want empty", gotPath)
	}
	if !gotMtime.IsZero() {
		t.Errorf("newestGoFile mtime = %v, want zero", gotMtime)
	}
}

// TestNewestGoFile_NonexistentDir returns ("", zero-time) without
// panicking when the directory doesn't exist.
func TestNewestGoFile_NonexistentDir(t *testing.T) {
	gotPath, gotMtime := newestGoFile("/nonexistent/path/that/does/not/exist")
	if gotPath != "" {
		t.Errorf("newestGoFile path = %q, want empty", gotPath)
	}
	if !gotMtime.IsZero() {
		t.Errorf("newestGoFile mtime = %v, want zero", gotMtime)
	}
}

// --- helpers ---

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustChtime(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}
