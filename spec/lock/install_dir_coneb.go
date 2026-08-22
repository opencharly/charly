package lock

// install_dir_coneb.go — the atomic directory-install primitive (renameat2 RENAME_EXCHANGE swap
// or plain rename), RELOCATED to the spec/lock fabric slice (#55 coneB build-render cone, Class A
// — a host filesystem primitive a plugin physically cannot hold, co-located with the flock family).
// The body is verbatim from sdk/kit/install_dir.go; sdk/kit re-exports it
// (var InstallDirAtomic = lock.InstallDirAtomic) so existing kit.InstallDirAtomic call sites
// (charly core's generate.go + build_stage_atomic_test.go, candy/plugin-build) are unchanged.
// New consumers reference spec/lock directly. Carries golang.org/x/sys/unix in its own slice
// (Rule 2).

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// InstallDirAtomic atomically installs the freshly-populated tmp directory as
// final. When final already exists, the two dirs are swapped in a single atomic
// renameat2(RENAME_EXCHANGE) — a concurrent reader of final always sees a
// complete dir (the old one before, the new one after) — and the swapped-out old
// content (now under tmp) is removed. When final is absent, a plain rename
// installs it. A lost create-race (a concurrent process installed identical
// content first) is benign: the redundant tmp is discarded. Linux-only
// (renameat2); the project targets Linux. Relocated from charly core (P8b) so the
// build render engine and charly's staging primitives share the one primitive.
func InstallDirAtomic(tmp, final string) error {
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
