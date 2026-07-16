package main

import "github.com/opencharly/sdk/deploykit"

// CollectOpts is the read-only input a `charly status` invocation threads
// through its host-side machinery: the deploy-cone data the nested-overlay
// tree builder (status_nested.go's buildStatusRootsTree, a SEPARATE
// still-host-side concern — its own future cutover) needs, plus the
// per-substrate collector inputs collectFlat resolves once and reuses for
// every word. Every substrate collector itself (pod/local/vm/k8s/android)
// now lives in the substrate plugin (P14a + K5) — none of them take
// CollectOpts any more; they receive their inputs over the OpStatusCollect
// wire request, re-hydrating the deploy-cone view they need (project via
// HostBuild("resolved-project"), per-machine via deploykit.LoadBundleConfig())
// for themselves. CollectOpts carries NO enginekit client (the engine shed
// from core with them).
type CollectOpts struct {
	IncludeAll bool                    // mirrors --all
	Nested     bool                    // mirrors --nested (live multi-hop probing of nested children + live k8s)
	Deploy     *deploykit.BundleConfig // ~/.config/charly/charly.yml (may be nil)
	Unified    *UnifiedFile            // charly.yml projection incl. folded kind:check beds (may be nil)
	RunMode    string                  // c.rt.RunMode
}
