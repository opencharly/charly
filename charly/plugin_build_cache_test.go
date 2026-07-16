package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPluginBuildEnvKeepsVCSStampingReadOnly(t *testing.T) {
	srcDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	env := pluginBuildEnv([]string{"HOME=/tmp/home", "GOWORK=wrong", "GIT_OPTIONAL_LOCKS=1"}, srcDir)
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "GOWORK=wrong") || strings.Contains(joined, "GIT_OPTIONAL_LOCKS=1") {
		t.Fatalf("plugin build environment retained conflicting settings:\n%s", joined)
	}
	if !strings.Contains(joined, "GOWORK=off") || !strings.Contains(joined, "GIT_OPTIONAL_LOCKS=0") {
		t.Fatalf("plugin build environment is missing its isolation settings:\n%s", joined)
	}
	if strings.Contains(joined, "GOFLAGS=-buildvcs=false") {
		t.Fatal("plugin build disabled VCS stamping")
	}
}

func TestFinalizeDeclaredKindConnectionsRetainsUnconnectedCause(t *testing.T) {
	const word = "test-unconnected-kind"
	original := declaredKindConnectErr
	declaredKindConnectErr = map[string]error{word: fmt.Errorf("original connection failure")}
	t.Cleanup(func() { declaredKindConnectErr = original })

	finalizeDeclaredKindConnections(map[string]struct{}{word: {}})
	if got := declaredKindConnectError(word); got == nil || got.Error() != "original connection failure" {
		t.Fatalf("unconnected declared kind lost its causal error: %v", got)
	}
}

// Different plugin binaries deliberately build concurrently: their per-output locks do not
// serialize them, so this exercises the shared-repository Git status probe that failed during a
// concurrent check roster. Output binaries stay in a test-local Charly cache; Go keeps using its
// normal configured build cache.
func TestBuildPluginBinary_DifferentPluginsConcurrent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	sources := []string{filepath.Join("..", "candy", "plugin-record"), filepath.Join("..", "candy", "plugin-wl")}
	for _, src := range sources {
		if _, err := os.Stat(filepath.Join(src, "go.mod")); err != nil {
			t.Fatalf("plugin fixture %s: %v", src, err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, len(sources))
	var ready sync.WaitGroup
	ready.Add(len(sources))
	for _, src := range sources {
		go func(src string) {
			ready.Done()
			<-start
			_, err := buildPluginBinary(context.Background(), src, "concurrent-plugin-"+filepath.Base(src))
			errs <- err
		}(src)
	}
	ready.Wait()
	close(start)
	for range sources {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent plugin build: %v", err)
		}
	}
}

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
