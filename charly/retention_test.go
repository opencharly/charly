package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func img(id, name, short, version string) LocalImageInfo {
	return LocalImageInfo{
		ID:    id,
		Names: []string{name},
		Labels: map[string]string{
			"ai.opencharly.box":     short,
			"ai.opencharly.version": version,
		},
	}
}

// TestPruneImagesByRetention covers grouping by short name, CalVer ordering,
// keep-newest-N, the in-use skip, and the "never touch unlabelled / undateable"
// guards. Uses dryRun so no real rmi runs.
func TestPruneImagesByRetention(t *testing.T) {
	origList, origCtr, origFloor := ListLocalImages, listContainerImageRefs, liveBuildFloor
	defer func() { ListLocalImages, listContainerImageRefs, liveBuildFloor = origList, origCtr, origFloor }()
	// Stub the build-activity floor to "no live build" so the retention decision under
	// test is deterministic regardless of a concurrent host build holding a lock in the
	// shared build-activity dir (the defect this fix closes — an unstubbed floor read the
	// host-global lock dir and made the test fail whenever another build ran concurrently).
	liveBuildFloor = func() (CalVer, bool, int) { return CalVer{}, false, 0 }

	ListLocalImages = func(string) ([]LocalImageInfo, error) {
		return []LocalImageInfo{
			img("aaa", "ghcr/foo:2026.001.0100", "foo", "2026.001.0100"), // oldest foo
			img("bbb", "ghcr/foo:2026.001.0200", "foo", "2026.001.0200"), // middle foo (mark in-use)
			img("ccc", "ghcr/foo:2026.001.0300", "foo", "2026.001.0300"), // newest foo (kept)
			img("ddd", "ghcr/bar:2026.001.0100", "bar", "2026.001.0100"), // sole bar (kept)
			{ID: "eee", Names: []string{"docker.io/other:latest"}},       // no charly label → ignored
		}, nil
	}
	// bbb is referenced by a container → must be skipped.
	listContainerImageRefs = func(string) (map[string]bool, map[string]bool, error) {
		return map[string]bool{"bbb": true}, map[string]bool{}, nil
	}

	removed, err := pruneImagesByRetention("podman", 1, true)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	sort.Strings(removed)
	// foo: keep newest (ccc); bbb in-use skipped; aaa removed. bar: only one, kept.
	want := []string{"ghcr/foo:2026.001.0100"}
	if len(removed) != len(want) || removed[0] != want[0] {
		t.Errorf("removed = %v, want %v", removed, want)
	}
}

// TestPruneImagesByRetention_SharedID is the regression guard for the
// keep_images over-removal bug: a content-stable image rebuilt many times has
// MANY CalVer tags all pointing at ONE image id. podman lists one row per tag,
// each row's Names listing every tag — model that worst case (pre-dedup input)
// to prove retention is per-TAG and never wipes the just-built/newest tag.
func TestPruneImagesByRetention_SharedID(t *testing.T) {
	origList, origCtr, origFloor := ListLocalImages, listContainerImageRefs, liveBuildFloor
	defer func() { ListLocalImages, listContainerImageRefs, liveBuildFloor = origList, origCtr, origFloor }()
	// Stub the build-activity floor to "no live build" so the retention decision under
	// test is deterministic regardless of a concurrent host build holding a lock in the
	// shared build-activity dir (the defect this fix closes — an unstubbed floor read the
	// host-global lock dir and made the test fail whenever another build ran concurrently).
	liveBuildFloor = func() (CalVer, bool, int) { return CalVer{}, false, 0 }

	allTags := []string{
		"ghcr/check-pod:2026.150.0827",
		"ghcr/check-pod:2026.150.0830",
		"ghcr/check-pod:2026.150.0835",
		"ghcr/check-pod:2026.150.0836",
		"ghcr/check-pod:2026.150.0916", // newest / just-built
	}
	rowPerTag := make([]LocalImageInfo, len(allTags))
	for i := range allTags {
		rowPerTag[i] = LocalImageInfo{
			ID:    "ccc", // all five tags share ONE image id
			Names: append([]string(nil), allTags...),
			Labels: map[string]string{
				"ai.opencharly.box":     "check-pod",
				"ai.opencharly.version": "2026.155.1801", // content-stable across tags
			},
		}
	}
	ListLocalImages = func(string) ([]LocalImageInfo, error) { return rowPerTag, nil }
	listContainerImageRefs = func(string) (map[string]bool, map[string]bool, error) {
		return map[string]bool{}, map[string]bool{}, nil
	}

	removed, err := pruneImagesByRetention("podman", 3, true)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	sort.Strings(removed)
	// keepN=3 keeps the newest 3 tags (.835/.836/.916); only the 2 oldest go.
	want := []string{"ghcr/check-pod:2026.150.0827", "ghcr/check-pod:2026.150.0830"}
	if len(removed) != len(want) || removed[0] != want[0] || removed[1] != want[1] {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
	// The just-built newest tag must NEVER be removed — this is the bug.
	for _, r := range removed {
		if r == "ghcr/check-pod:2026.150.0916" {
			t.Fatalf("BUG: removed the newest/just-built tag %q", r)
		}
	}
}

func TestPruneImagesByRetention_Disabled(t *testing.T) {
	called := false
	origList := ListLocalImages
	defer func() { ListLocalImages = origList }()
	ListLocalImages = func(string) ([]LocalImageInfo, error) { called = true; return nil, nil }

	removed, err := pruneImagesByRetention("podman", 0, true)
	if err != nil || removed != nil {
		t.Errorf("keep=0 should no-op, got removed=%v err=%v", removed, err)
	}
	if called {
		t.Error("keep=0 should not even enumerate images")
	}
}

// TestPruneCheckRuns covers keep-newest-N of CalVer run dirs + result files, the
// runs/<id> mtime path, and the NOTES.md preservation invariant.
func TestPruneCheckRuns(t *testing.T) {
	root := t.TempDir()
	bed := filepath.Join(root, "sample-bed")
	// 3 CalVer run dirs (newest = 2026.143.0300) + NOTES.md.
	for _, cv := range []string{"2026.143.0100", "2026.143.0200", "2026.143.0300"} {
		mustMkdir(t, filepath.Join(bed, cv))
	}
	mustWrite(t, filepath.Join(bed, "NOTES.md"), "memory")
	// A score dir with result files + runs/<id>.
	score := filepath.Join(root, "default")
	mustMkdir(t, score)
	for _, r := range []string{"result-2026.143.0100.yml", "result-2026.143.0200.yml", "result-2026.143.0300.yml"} {
		mustWrite(t, filepath.Join(score, r), "x")
	}
	mustWrite(t, filepath.Join(score, "NOTES.md"), "memory")

	removed, err := pruneCheckRuns(root, 1, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(removed) != 4 { // 2 old bed dirs + 2 old result files
		t.Errorf("removed %d, want 4: %v", len(removed), removed)
	}
	// Newest kept, oldest gone, NOTES.md preserved.
	assertExists(t, filepath.Join(bed, "2026.143.0300"))
	assertGone(t, filepath.Join(bed, "2026.143.0100"))
	assertExists(t, filepath.Join(bed, "NOTES.md"))
	assertExists(t, filepath.Join(score, "result-2026.143.0300.yml"))
	assertGone(t, filepath.Join(score, "result-2026.143.0100.yml"))
	assertExists(t, filepath.Join(score, "NOTES.md"))
}

func TestPruneCheckRuns_DryRunAndDisabled(t *testing.T) {
	root := t.TempDir()
	bed := filepath.Join(root, "bed")
	for _, cv := range []string{"2026.143.0100", "2026.143.0200"} {
		mustMkdir(t, filepath.Join(bed, cv))
	}
	// dry-run lists but deletes nothing.
	removed, err := pruneCheckRuns(root, 1, true)
	if err != nil || len(removed) != 1 {
		t.Fatalf("dry-run removed=%v err=%v, want 1 listed", removed, err)
	}
	assertExists(t, filepath.Join(bed, "2026.143.0100")) // still there

	// keep=0 disables.
	r2, _ := pruneCheckRuns(root, 0, false)
	if r2 != nil {
		t.Errorf("keep=0 should no-op, got %v", r2)
	}
}

func assertExists(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("expected %s to exist: %v", p, err)
	}
}

func assertGone(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("expected %s to be gone, stat err=%v", p, err)
	}
}

// TestPruneBuildCandyDirs covers the versioned .build/_candy/<candy>.<version>/
// retention (keep newest N per candy) + the legacy .build/_layers/ removal.
func TestPruneBuildCandyDirs(t *testing.T) {
	buildDir := t.TempDir()
	candyRoot := filepath.Join(buildDir, "_candy")
	mk := func(rel string) {
		if err := os.MkdirAll(filepath.Join(buildDir, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Two candies with three versions each + a transient temp + a legacy _layers.
	for _, v := range []string{"2026.167.1000", "2026.167.1100", "2026.167.1200"} {
		mk("_candy/alpha." + v)
		mk("_candy/beta." + v)
	}
	mk("_candy/.alpha.tmp.XYZ") // transient in-flight install — must be ignored
	mk("_layers/cuda")          // legacy shared staging — must be removed

	removed := pruneBuildCandyDirs(buildDir, 1, false) // keep newest 1 per candy

	// Legacy _layers gone.
	if _, err := os.Stat(filepath.Join(buildDir, "_layers")); !os.IsNotExist(err) {
		t.Errorf("legacy _layers should be removed")
	}
	// Newest kept, older removed, per candy.
	for _, c := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(candyRoot, c+".2026.167.1200")); err != nil {
			t.Errorf("%s newest version should be kept: %v", c, err)
		}
		for _, old := range []string{"2026.167.1000", "2026.167.1100"} {
			if _, err := os.Stat(filepath.Join(candyRoot, c+"."+old)); !os.IsNotExist(err) {
				t.Errorf("%s.%s should be pruned", c, old)
			}
		}
	}
	// Transient temp untouched.
	if _, err := os.Stat(filepath.Join(candyRoot, ".alpha.tmp.XYZ")); err != nil {
		t.Errorf("transient temp dir should be ignored, not removed: %v", err)
	}
	// 5 removals: legacy _layers + 2 old alpha + 2 old beta.
	if len(removed) != 5 {
		t.Errorf("removed %d, want 5: %v", len(removed), removed)
	}
}
