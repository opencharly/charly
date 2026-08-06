package docs

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// pruneGeneratedPages deletes every page carrying the generated header before a run emits its
// replacements.
//
// Without it the generator can only ever ADD. It writes each page it knows about and never looks
// at what is already there, so a page it no longer emits — one whose source was deleted, whose
// generator was removed, or which was renamed — stays in the tree forever, and every gate above it
// reports a pass:
//
//   - `task docs:drift` compares the committed tree against a regeneration. A stale page is
//     identical in both, so it is not drift.
//   - `verifySiteLinks` walks the emitted tree, so links INTO the stale page still resolve.
//
// That combination is worse than a missing check, because it reads as proof. It was measured, not
// theorised: removing the generateGrievances call and re-running `task docs:sync` reported
// "already up to date (regeneration is a no-op)" and exit 0 while the generator no longer produced
// the page at all.
//
// Pruning first rather than diffing afterwards keeps this to one function and no threaded state:
// generation rewrites every page it still emits, so whatever does not come back was not emitted.
// A generation that fails midway therefore leaves visible holes rather than a tree that looks
// complete — which is the honest outcome, since a failed run has not proved anything.
//
// The header is the whole safety boundary. Hand-authored pages (`start/`, `concepts/`, `guides/`)
// do not carry it and are never touched; a file is read and matched, never inferred from its path.
func pruneGeneratedPages(outRoot string) (int, error) {
	header := []byte(generatedHeader)
	var stale []string

	err := filepath.WalkDir(outRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".mdx":
		default:
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if bytes.Contains(raw, header) {
			stale = append(stale, path)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scan --out for generated pages: %w", err)
	}

	for _, path := range stale {
		if err := os.Remove(path); err != nil {
			return 0, fmt.Errorf("remove stale page %s: %w", path, err)
		}
	}

	// Directories that held only generated pages are now empty, and an empty directory in a
	// content collection is noise at best. Remove them bottom-up; a directory that still holds a
	// hand-authored page fails the rmdir and is left alone.
	pruneEmptyDirs(outRoot)

	return len(stale), nil
}

// pruneEmptyDirs removes empty directories under root, deepest first. root itself is never
// removed. Failures are ignored on purpose: the only way this fails is a non-empty directory,
// which is exactly the case that must be left alone.
func pruneEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	for _, dir := range slices.Backward(dirs) {
		_ = os.Remove(dir)
	}
}
