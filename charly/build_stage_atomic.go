package main

// build_stage_atomic.go — race-free, deterministic .build/ staging primitives.
//
// The .build/ tree is SHARED by every concurrent `charly box build` /
// `charly box generate` in one project dir (the _candy staging dirs + each
// image's Containerfile). To let parallel beds fan out without a per-dir build
// lock (serializing cold builds is catastrophic for wall-clock), every shared
// write is made ATOMIC + IDEMPOTENT instead: a concurrent reader (a podman build
// COPYing from _candy, or reading a Containerfile) always sees a COMPLETE,
// deterministic artifact — never a half-removed dir (the `directory not empty` /
// `no such file` race) or a partially-written file. Identical inputs always
// produce identical bytes, so podman's content+instruction-keyed cache hits.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// atomicWriteFile writes data to path atomically: a temp file in the SAME dir
// (same filesystem, so rename is atomic) is written, chmod'd, then renamed over
// path. A concurrent reader sees either the old complete file or the new complete
// file, never a partial write; concurrent writers of identical content converge
// (last rename wins, bytes identical). Replaces a plain os.WriteFile wherever the
// target may be read by a concurrent build.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("atomic write %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed away
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("atomic write %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("atomic write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("atomic write %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("atomic write %s: %w", path, err)
	}
	return nil
}

// installDirAtomic atomically installs the freshly-populated tmp directory as
// final. When final already exists, the two dirs are swapped in a single atomic
// renameat2(RENAME_EXCHANGE) — a concurrent reader of final always sees a
// complete dir (the old one before, the new one after) — and the swapped-out old
// content (now under tmp) is removed. When final is absent, a plain rename
// installs it. A lost create-race (a concurrent process installed identical
// content first) is benign: the redundant tmp is discarded. Linux-only
// (renameat2); the project targets Linux.
func installDirAtomic(tmp, final string) error {
	// Try the atomic swap first — the common case is that final exists from a
	// prior generate (re-runs refresh content this way, race-free).
	err := unix.Renameat2(unix.AT_FDCWD, tmp, unix.AT_FDCWD, final, unix.RENAME_EXCHANGE)
	if err == nil {
		return os.RemoveAll(tmp) // tmp now holds the old content
	}
	if !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("atomic swap %s: %w", final, err)
	}
	// final did not exist (RENAME_EXCHANGE → ENOENT). Create it by plain rename.
	if rerr := os.Rename(tmp, final); rerr == nil {
		return nil
	}
	// Lost the create-race to a concurrent process that installed identical
	// content first — discard the redundant tmp.
	return os.RemoveAll(tmp)
}
