package spec

// loader_discover.go — the kind-blind config-loader discover-walk LEAF PRIMITIVES:
// FindEntityDirs (scan a discover root for directories carrying a manifest),
// DiscoverSkipDir (the VCS/build-artifact skip-list), and the two os.Stat
// helpers FindEntityDirs uses. Relocated here (FLOOR-SLIM axis-A mechanical
// batch, zero logic change — the sibling of node_helpers.go's ClassifyDoc
// relocation) from sdk/kit so charly core's prescan (plugin_prescan.go, the
// loader-walk Boundary seam — a core M prescan-dispatch) can call it without
// importing a mechanism kit; aliased in sdk/kit (loader_discover.go) so every
// existing kit.FindEntityDirs / kit.DiscoverSkipDir / kit.DirExists /
// kit.FileExists call site (sdk/loaderkit's discover.go/walk.go,
// sdk/deploykit, candy/plugin-box, sdk/kit/scaffold.go) keeps compiling
// unchanged (R3 — ONE copy, in spec now).

import (
	"os"
	"path/filepath"
	"sort"
)

// FileExists reports whether path exists and is a regular (non-dir) file.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// DirExists reports whether path exists and is a directory.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// DiscoverSkipDir reports whether a directory name is a VCS or build-artifact
// dir that never contains a discoverable charly.yml manifest — skipped by the
// discover walk both for speed and to avoid traversing concurrently-mutated
// build outputs (e.g. a candy's pkgbuild/{pkg,src} under a live makepkg).
func DiscoverSkipDir(name string) bool {
	switch name {
	case ".git", ".build", "output", "node_modules":
		return true
	}
	return false
}

// FindEntityDirs walks a scan root and returns every directory that contains
// the given canonical filename. When recursive is false, only the immediate
// children of path are considered.
func FindEntityDirs(path, filename string, recursive bool) ([]string, error) {
	if !DirExists(path) {
		// A discover path that doesn't exist yields zero entities — NOT an
		// error. discover: is universally applied at load now (not just on the
		// candy path), and a project may legitimately declare a uniform
		// `discover: [box, candy]` while carrying only one of the directories
		// (e.g. a distro submodule with boxes but no candy/ of its own).
		return nil, nil
	}
	var out []string
	if !recursive {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			target := filepath.Join(path, e.Name(), filename)
			if FileExists(target) {
				out = append(out, filepath.Join(path, e.Name()))
			}
		}
		sort.Strings(out)
		return out, nil
	}
	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			// A per-entry error must NOT abort the whole discover walk: a
			// discoverable manifest can never live in an unreadable directory,
			// and under concurrency a SIBLING build's transient artifact (a
			// makepkg fakeroot-owned `pkg/` under a candy's pkgbuild/) yields a
			// passing EACCES that would otherwise fail EVERY concurrent
			// LoadUnified. Skip the offending entry/subtree and continue; only the
			// scan root itself (info == nil) is a real, propagated error.
			if info == nil {
				return err
			}
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			// VCS + build-artifact dirs never hold a discoverable manifest;
			// skipping them avoids both the wasted traversal AND the
			// concurrent-build race for the common cases.
			if DiscoverSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Name() == filename {
			out = append(out, filepath.Dir(p))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
