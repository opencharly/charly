package main

import (
	"testing"
)

// refs_fresh_test.go — what REMAINS in charly of the refs-backend guards after K-wave 2 cone R1
// moved the fetch orchestration into candy/plugin-loader.
//
// TestEnsureRepoDownloaded_MutableRefAlwaysDelegates and TestIsMutableRefCoreContract moved to
// candy/plugin-loader/refs_fetch_test.go, where the cache-hit-vs-delegate decision they guard now
// lives and can still be driven with a stub Downloader. They cannot stay here: charly no longer has
// an activeRefsDownloader package variable to swap, because it is no longer a party to the fetch.

// TestRefsProviderRegisteredAtInit is the SURVIVING half of the former
// TestRefsDownloaderRegisteredAtInit, restated against the wiring that actually exists now.
//
// The old assertion — that charly's activeRefsDownloader is non-nil at test time — proved that the
// dropped kit.DefaultDownloader{} bootstrap default was safe, because a compiled-in candy/plugin-refs
// registered the typed handle at init() before any @github resolution could run. That variable is
// gone; charly stopped resolving the backend when candy/plugin-loader took over the orchestration
// and started reaching it as a peer via InvokeProvider(class:"refs", word:"refs").
//
// The INVARIANT the old test really protected is unchanged and still worth pinning: the refs backend
// must be resolvable before the first fetch, so no @github resolution can observe a missing one.
// Its modern form is that the provider is REGISTERED at init — which is exactly what the loader
// plugin's InvokeProvider will look up. This FAILS if plugin-refs stops registering or is dropped
// from compiled_plugins:, the same regressions the old test caught.
func TestRefsProviderRegisteredAtInit(t *testing.T) {
	if _, ok := providerRegistry.resolve(ClassRefs, "refs"); !ok {
		t.Fatal("candy/plugin-refs must register refs:refs at init (before any resolution) — absent means the bootstrap fetch backend is unreachable")
	}
}
