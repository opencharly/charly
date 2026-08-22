package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestPIDsHoldingPath_FindsRealHolder is the discriminating test: it starts a real
// process holding a real file open and requires that exact PID back. A stub that
// always returned nil, or one that matched by path string against a spelling the
// child never used, both fail here.
func TestPIDsHoldingPath_FindsRealHolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "held.lock")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seeding lock file: %v", err)
	}

	// `sh -c 'exec 9<file; sleep 30'` holds the file on fd 9 without writing to it —
	// the same shape as an flock holder, which writes nothing.
	cmd := exec.Command("sh", "-c", "exec 9<"+path+"; sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting holder: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	want := cmd.Process.Pid
	deadline := time.Now().Add(10 * time.Second)
	for {
		pids := PIDsHoldingPath(path)
		if slices.Contains(pids, want) {
			return // found it
		}
		if time.Now().After(deadline) {
			t.Fatalf("PIDsHoldingPath(%s) = %v, never contained the holder pid %d", path, pids, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPIDsHoldingPath_UnheldFileHasNoHolders asserts the OTHER direction, which is the
// one a stop verb acts on: an unheld file must report nobody. A scanner that matched
// too loosely — by name, by directory, by any-open-fd-in-the-tree — would report a
// holder here and make `check stop` signal an unrelated process.
func TestPIDsHoldingPath_UnheldFileHasNoHolders(t *testing.T) {
	dir := t.TempDir()
	held := filepath.Join(dir, "held.lock")
	unheld := filepath.Join(dir, "unheld.lock")
	for _, p := range []string{held, unheld} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatalf("seeding %s: %v", p, err)
		}
	}

	// A holder on a SIBLING file in the SAME directory — the near-miss that a
	// directory-scoped or name-scoped match would wrongly attribute to `unheld`.
	cmd := exec.Command("sh", "-c", "exec 9<"+held+"; sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting holder: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// Wait until the sibling holder is definitely visible, so this is a real
	// discrimination rather than a race that passes by arriving early.
	deadline := time.Now().Add(10 * time.Second)
	for !slices.Contains(PIDsHoldingPath(held), cmd.Process.Pid) {
		if time.Now().After(deadline) {
			t.Fatal("sibling holder never became visible; test cannot discriminate")
		}
		time.Sleep(50 * time.Millisecond)
	}

	if pids := PIDsHoldingPath(unheld); len(pids) != 0 {
		t.Fatalf("PIDsHoldingPath(unheld) = %v, want empty while only a sibling is held", pids)
	}
}

// TestPIDsHoldingPath_MissingFileIsEmpty — a bed that never ran has no lock file at
// all, and that must read as "no run in flight" rather than as an error.
func TestPIDsHoldingPath_MissingFileIsEmpty(t *testing.T) {
	if pids := PIDsHoldingPath(filepath.Join(t.TempDir(), "never-created")); len(pids) != 0 {
		t.Fatalf("PIDsHoldingPath(missing) = %v, want empty", pids)
	}
}
