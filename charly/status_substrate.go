package main

import "github.com/opencharly/sdk/deploykit"

// CollectOpts is the read-only input a `charly status` invocation threads
// through its host-side machinery: the ONE remaining host-side deploy-cone
// consumer is enrichVmRow (the vm-row SSH-port/network enrichment), which
// needs the per-machine deploy config; RunMode feeds the per-substrate
// collector inputs collectFlat resolves once and reuses for every word.
// The declared-nested-tree pre-resolution (formerly status_nested.go's
// buildStatusRootsTree) moved PLUGIN-SIDE (candy/plugin-status/nested_tree.go,
// K5) — it no longer reads CollectOpts, so Nested/Unified were dropped.
// Every substrate collector itself (pod/local/vm/k8s/android) lives in the
// substrate plugin (P14a + K5) — none of them take CollectOpts any more;
// they receive their inputs over the OpStatusCollect wire request,
// re-hydrating the deploy-cone view they need (project via
// HostBuild("resolved-project"), per-machine via deploykit.LoadBundleConfig())
// for themselves. CollectOpts carries NO enginekit client (the engine shed
// from core with them).
type CollectOpts struct {
	IncludeAll bool                    // mirrors --all
	Deploy     *deploykit.BundleConfig // ~/.config/charly/charly.yml (may be nil)
	RunMode    string                  // c.rt.RunMode
}
