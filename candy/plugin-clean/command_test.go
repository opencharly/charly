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

// TestCleanCategories covers the --images/--check/--deep flag-resolution logic: the pre-existing
// "any one of --images/--check given alone suppresses the other default categories" behavior stays
// unchanged, and --deep NEVER fires implicitly on a plain `charly clean` (R5: no default-behavior
// change) but joins the same "an explicit category was given" gate as --images/--check.
func TestCleanCategories(t *testing.T) {
	cases := []struct {
		name                            string
		images, check, deep             bool
		wantImages, wantCheck, wantDeep bool
		wantMakepkg                     bool
	}{
		{name: "no flags: full default sweep, deep excluded",
			images: false, check: false, deep: false,
			wantImages: true, wantCheck: true, wantDeep: false, wantMakepkg: true},
		{name: "--images alone: only images",
			images: true, check: false, deep: false,
			wantImages: true, wantCheck: false, wantDeep: false, wantMakepkg: false},
		{name: "--check alone: only check",
			images: false, check: true, deep: false,
			wantImages: false, wantCheck: true, wantDeep: false, wantMakepkg: false},
		{name: "--images + --check: both, no makepkg",
			images: true, check: true, deep: false,
			wantImages: true, wantCheck: true, wantDeep: false, wantMakepkg: false},
		{name: "--deep alone: only deep",
			images: false, check: false, deep: true,
			wantImages: false, wantCheck: false, wantDeep: true, wantMakepkg: false},
		{name: "--deep + --images: both, check/makepkg excluded",
			images: true, check: false, deep: true,
			wantImages: true, wantCheck: false, wantDeep: true, wantMakepkg: false},
		{name: "--deep + --check: both, images/makepkg excluded",
			images: false, check: true, deep: true,
			wantImages: false, wantCheck: true, wantDeep: true, wantMakepkg: false},
		{name: "all three flags: everything but makepkg",
			images: true, check: true, deep: true,
			wantImages: true, wantCheck: true, wantDeep: true, wantMakepkg: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotImages, gotCheck, gotDeep, gotMakepkg := cleanCategories(c.images, c.check, c.deep)
			if gotImages != c.wantImages || gotCheck != c.wantCheck || gotDeep != c.wantDeep || gotMakepkg != c.wantMakepkg {
				t.Errorf("cleanCategories(%v,%v,%v) = (%v,%v,%v,%v), want (%v,%v,%v,%v)",
					c.images, c.check, c.deep,
					gotImages, gotCheck, gotDeep, gotMakepkg,
					c.wantImages, c.wantCheck, c.wantDeep, c.wantMakepkg)
			}
		})
	}
}
