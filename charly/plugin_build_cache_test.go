package main

import (
	"path/filepath"
	"testing"
)

// TestPluginSourceTag_DistinctSrcDirs is the #76 regression test (the plugin-build-cache analogue
// of #75's TestBedRunImageTag): two worktrees building the SAME plugin word from DIFFERENT source
// dirs MUST land distinct cache paths, so they never race the one shared `go build -o <bin>` output
// file. The plugin cache (~/.cache/charly/plugins/) was word-keyed shared-mutable state; scoping the
// path by source dir (#76) makes each worktree's build its own file. Pinned at the pure helper level
// — the cache path is `safePluginBinName(name)+"-"+pluginSourceTag(srcDir)`, so distinct srcDirs
// producing distinct tags IS the collision-prevention invariant.
func TestPluginSourceTag_DistinctSrcDirs(t *testing.T) {
	name := "plugin-vm"
	wtA := filepath.Join("/home", "atrawog", "Atrapub", "o", "charly-wt-vmlife", "candy", "plugin-vm")
	wtB := filepath.Join("/home", "atrawog", "Atrapub", "o", "charly-wt-k4", "candy", "plugin-vm")
	tagA := pluginSourceTag(wtA)
	tagB := pluginSourceTag(wtB)
	if tagA == tagB {
		t.Fatalf("pluginSourceTag: distinct srcDirs (%s vs %s) produced the SAME tag %q — the #76 cross-worktree collision is not prevented", wtA, wtB, tagA)
	}
	pathA := filepath.Join(pluginBuildCacheDir(), safePluginBinName(name)+"-"+tagA)
	pathB := filepath.Join(pluginBuildCacheDir(), safePluginBinName(name)+"-"+tagB)
	if pathA == pathB {
		t.Fatalf("distinct srcDirs produced the SAME cache path %q — the #76 collision is not prevented", pathA)
	}
}

// TestPluginSourceTag_SameSrcDirStable proves the tag is STABLE for the same source dir (so a
// re-build within one worktree reuses the same cache path + the per-binary lock serializes it,
// never spawning a new path per invocation). A non-deterministic tag would defeat the lock and
// litter the cache with one file per build.
func TestPluginSourceTag_SameSrcDirStable(t *testing.T) {
	src := filepath.Join("/home", "x", "candy", "plugin-appium")
	a := pluginSourceTag(src)
	b := pluginSourceTag(src)
	if a != b {
		t.Fatalf("pluginSourceTag: same srcDir %s produced unstable tags %q vs %q — the cache path must be stable across re-builds", src, a, b)
	}
	if len(a) != 16 {
		t.Fatalf("pluginSourceTag: expected a 16-hex-char tag, got %q (len %d)", a, len(a))
	}
}