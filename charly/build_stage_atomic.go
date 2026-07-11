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

	"github.com/opencharly/sdk/kit"
	"golang.org/x/sys/unix"
)

// atomicWriteFile → kit.AtomicWriteFile (P8 shim — the pure atomic-write primitive
// moved to sdk/kit so the build render engine shares it).
var atomicWriteFile = kit.AtomicWriteFile

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
