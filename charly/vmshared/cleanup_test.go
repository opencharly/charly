package vmshared

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// resetCleanupState wipes the temp-cleanup registry between tests so
// state from one test doesn't leak into another. Required because the
// registry is package-global.
func resetCleanupState(t *testing.T) {
	t.Helper()
	tempCleanupsMu.Lock()
	tempCleanups = map[string]struct{}{}
	tempCleanupsMu.Unlock()
}

func TestRegisterAndRunCleanups(t *testing.T) {
	resetCleanupState(t)
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
		RegisterTempCleanup(p)
	}
	runRegisteredCleanups()
	for _, p := range []string{a, b} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s removed, stat err = %v", p, err)
		}
	}
}

func TestRegisterIdempotent(t *testing.T) {
	resetCleanupState(t)
	p := filepath.Join(t.TempDir(), "x")
	RegisterTempCleanup(p)
	RegisterTempCleanup(p)
	RegisterTempCleanup(p)
	tempCleanupsMu.Lock()
	n := len(tempCleanups)
	tempCleanupsMu.Unlock()
	if n != 1 {
		t.Errorf("registry size = %d, want 1 (deduped)", n)
	}
}

func TestUnregisterRemovesFromRegistry(t *testing.T) {
	resetCleanupState(t)
	p := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	RegisterTempCleanup(p)
	UnregisterTempCleanup(p)
	runRegisteredCleanups()
	// File must still exist — the unregister means the signal handler
	// no longer claims responsibility for it.
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file should still exist after unregister, stat = %v", err)
	}
}

// makeStaleTemp creates a /tmp/<prefix><suffix> file whose mtime is set
// to age ago. Returns the path. Test cleans up via t.Cleanup.
func makeStaleTemp(t *testing.T, prefix, suffix string, age time.Duration) string {
	t.Helper()
	p := filepath.Join("/tmp", prefix+suffix)
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatalf("create %s: %v", p, err)
	}
	t.Cleanup(func() { _ = os.Remove(p) })
	old := time.Now().Add(-age)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatalf("chtimes %s: %v", p, err)
	}
	return p
}

func TestSweep_OldNotHeld_Removed(t *testing.T) {
	resetCleanupState(t)
	p := makeStaleTemp(t, "charly-merge-", "fakestale-9911-test.tar", 10*time.Minute)
	SweepStaleTemps()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("expected swept (mtime > 5min, no holder), stat = %v", err)
	}
}

func TestSweep_RecentNotHeld_Kept(t *testing.T) {
	resetCleanupState(t)
	// 1-minute-old: under the 5-minute safety floor.
	p := makeStaleTemp(t, "charly-merge-", "fresh-9912-test.tar", 1*time.Minute)
	SweepStaleTemps()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("expected kept (under safety floor), stat = %v", err)
	}
}

func TestSweep_HeldByCurrentProcess_Kept(t *testing.T) {
	resetCleanupState(t)
	p := makeStaleTemp(t, "charly-merge-", "held-9913-test.tar", 10*time.Minute)
	// Open the file and KEEP the handle so /proc/self/fd reports it.
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	SweepStaleTemps()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("expected kept (held open by current process), stat = %v", err)
	}
}

func TestSweep_NonCharlyFile_Untouched(t *testing.T) {
	resetCleanupState(t)
	// Doesn't match any sweepablePatterns prefix.
	p := makeStaleTemp(t, "junk-", "9914-test.tar", 10*time.Minute)
	SweepStaleTemps()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("expected non-charly file kept, stat = %v", err)
	}
}

func TestSweep_TunnelSocketIgnored(t *testing.T) {
	resetCleanupState(t)
	// The charly-tunnel- prefix is intentionally NOT in sweepablePatterns —
	// SSH-tunnel sockets are session-scoped persistent.
	p := makeStaleTemp(t, "charly-tunnel-", "9915-test.sock", 10*time.Minute)
	SweepStaleTemps()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("expected tunnel socket kept (not in sweep patterns), stat = %v", err)
	}
}

func TestSweep_AllPrefixesCovered(t *testing.T) {
	resetCleanupState(t)
	// Every prefix in sweepablePatterns should match a synthetic stale
	// file. This catches typos in the patterns slice.
	for _, prefix := range sweepablePatterns {
		p := makeStaleTemp(t, prefix, "9916-test.x", 10*time.Minute)
		SweepStaleTemps()
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("prefix %q: expected swept, stat = %v", prefix, err)
		}
	}
}
