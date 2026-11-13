package main

// plugin_build_testhelper_test.go — the ONE host-build entry point the out-of-process plugin e2e
// suites use now that the loader no longer owns a build-cache cluster.
//
// BACKGROUND. Those suites used to call the loader's package-main buildPluginBinary directly. The
// phase-1 store cutover (charly#896) deleted that function along with the rest of the cluster —
// pluginBuildCacheDir / pluginSourceTag / the `bin+".lock"` inside the cache / the `.stamp` sidecar
// in plugin_build_stamp.go — so the suites must be repointed at the ONE remaining implementation,
// sdk/buildcache.Build. Going through this helper rather than naming buildcache in eight files keeps
// the STORE ISOLATION in one place: the build cache root is $XDG_CACHE_HOME/charly with
// $CHARLY_CACHE_DIR as the sole override (storekit.CacheDir), and a test that built into the
// operator's real cache would be a write outside the temp dir (D-STORE-8).
//
// The returned path is the store's hardlink VIEW — the same thing the loader hands to
// LocalTransport — so a suite asserting "the host-built binary connects" still exercises exactly the
// production artifact.

import (
	"context"
	"testing"

	"github.com/opencharly/sdk/buildcache"
)

// hostBuildPluginForTest host-builds an out-of-process plugin module through the production build
// cache (buildcache.Build) with a test-local cache root, and returns the served binary's view path.
func hostBuildPluginForTest(t *testing.T, ctx context.Context, srcDir, name string) string {
	t.Helper()
	if srcDir == "" {
		t.Fatal("hostBuildPluginForTest: empty source dir")
	}
	isolatePluginSearch(t, "")
	res, err := buildcache.Build(ctx, buildcache.Request{SrcDir: srcDir, Name: name})
	if err != nil {
		t.Fatalf("buildcache.Build(%s): %v", name, err)
	}
	if res.ViewPath == "" {
		t.Fatalf("buildcache.Build(%s) returned an empty view path", name)
	}
	return res.ViewPath
}
