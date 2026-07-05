package clean

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCleanMakepkgArtifacts covers the pkg/arch makepkg sweep the plugin owns (moved from charly
// core): src/, pkg/, *.pkg.tar.zst, *.log are transient waste and removed; PKGBUILD must survive.
func TestCleanMakepkgArtifacts(t *testing.T) {
	root := t.TempDir()
	arch := filepath.Join(root, "pkg", "arch")
	if err := os.MkdirAll(filepath.Join(arch, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(arch, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(arch, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("opencharly-git-2026.001.0001-1-x86_64.pkg.tar.zst", "z")
	write("build.log", "l")
	write("PKGBUILD", "keep") // must survive

	removed := cleanMakepkgArtifacts(root, false)
	if len(removed) != 4 {
		t.Errorf("removed %d, want 4 (src, pkg, .zst, .log): %v", len(removed), removed)
	}
	if _, err := os.Stat(filepath.Join(arch, "src")); !os.IsNotExist(err) {
		t.Errorf("src should be gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(arch, "PKGBUILD")); err != nil {
		t.Errorf("PKGBUILD must survive: %v", err)
	}

	// Dry-run touches nothing but still reports.
	root2 := t.TempDir()
	a2 := filepath.Join(root2, "pkg", "arch")
	_ = os.MkdirAll(filepath.Join(a2, "src"), 0o755)
	if got := cleanMakepkgArtifacts(root2, true); len(got) != 1 {
		t.Errorf("dry-run: reported %d, want 1 (src)", len(got))
	}
	if _, err := os.Stat(filepath.Join(a2, "src")); err != nil {
		t.Errorf("dry-run must NOT remove src: %v", err)
	}
}
