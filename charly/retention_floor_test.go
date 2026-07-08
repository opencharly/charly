package main

import (
	"os"
	"path/filepath"
	"testing"
)

// withBuildActivityDir points the build-activity locks at a temp dir via
// XDG_CACHE_HOME (buildActivityDir derives from os.UserCacheDir).
func withBuildActivityDir(t *testing.T) string {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	return filepath.Join(cache, "charly", "locks", "builds")
}

// TestBuildActivityLock_FloorLifecycle proves the whole floor mechanism: a held
// activity lock is LIVE and floors retention at its recorded CalVer; release
// removes it; a stale (unheld) lock file is reaped by the next floor scan.
func TestBuildActivityLock_FloorLifecycle(t *testing.T) {
	dir := withBuildActivityDir(t)

	// No locks → no live builds.
	if _, ok, live := liveBuildFloor(); ok || live != 0 {
		t.Fatalf("empty dir: want no floor/live, got ok=%v live=%d", ok, live)
	}

	rel, err := acquireBuildActivityLock("2026.188.1900")
	if err != nil {
		t.Fatal(err)
	}
	floor, ok, live := liveBuildFloor()
	if !ok || live != 1 {
		t.Fatalf("held lock: want floorOK live=1, got ok=%v live=%d", ok, live)
	}
	if got := floor.String(); got != "2026.188.1900" {
		t.Fatalf("floor: want 2026.188.1900, got %s", got)
	}

	// A second, OLDER build lowers the floor.
	rel2, err := acquireBuildActivityLock("2026.188.1830")
	if err != nil {
		t.Fatal(err)
	}
	if floor, _, live = func() (CalVer, bool, int) { f, o, l := liveBuildFloor(); _ = o; return f, o, l }(); live != 2 || floor.String() != "2026.188.1830" {
		t.Fatalf("two live: want floor 2026.188.1830 live=2, got %s live=%d", floor, live)
	}
	if err := rel2(); err != nil {
		t.Fatal(err)
	}
	if err := rel(); err != nil {
		t.Fatal(err)
	}
	if _, ok, live := liveBuildFloor(); ok || live != 0 {
		t.Fatalf("after release: want none, got ok=%v live=%d", ok, live)
	}

	// A stale lock file (no holder) is reaped, never counted live.
	stale := filepath.Join(dir, "build-1-1.lock")
	if err := os.WriteFile(stale, []byte("2026.188.0001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, live := liveBuildFloor(); ok || live != 0 {
		t.Fatalf("stale lock: want reaped, got ok=%v live=%d", ok, live)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale lock file must be removed by the scan")
	}
}

// TestRetentionRemovable pins the pure retention decision, especially the
// build-activity protections that close the retention-untag race.
func TestRetentionRemovable(t *testing.T) {
	cv := func(s string) CalVer {
		v, ok := ParseCalVer(s)
		if !ok {
			t.Fatalf("bad calver %s", s)
		}
		return v
	}
	old := imageTagInfo{Ref: "r:old", ID: "id1", TagCalVer: cv("2026.188.1700"), OkTag: true}
	pinned := imageTagInfo{Ref: "r:pinned", ID: "id1", TagCalVer: cv("2026.188.1839"), OkTag: true}
	floor := cv("2026.188.1830")

	cases := []struct {
		name    string
		c       imageTagInfo
		idx     int
		live    int
		floorOK bool
		lastTag bool
		want    bool
	}{
		{"kept-newest", pinned, 2, 0, false, false, false},         // idx < keepN
		{"quiet-prune-removes-old", old, 5, 0, false, false, true}, // no live builds: standing rules only
		{"floor-protects-pin", pinned, 5, 1, true, false, false},   // >= floor while live
		{"floor-allows-older", old, 5, 1, true, false, true},       // < floor: untag ok while live
		{"unknown-floor-protects-all", old, 5, 1, false, false, false},
		{"last-tag-never-deleted-while-live", old, 5, 1, true, true, false},
		{"last-tag-ok-when-quiet", old, 5, 0, false, true, true},
		{"in-use-always-kept", imageTagInfo{Ref: "r", InUse: true, OkTag: true, TagCalVer: cv("2026.100.0001")}, 5, 0, false, false, false},
		{"undatable-always-kept", imageTagInfo{Ref: "r"}, 5, 0, false, false, false},
	}
	for _, tc := range cases {
		if got := retentionRemovable(tc.c, tc.idx, 3, floor, tc.floorOK, tc.live, tc.lastTag); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
