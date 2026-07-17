package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// status_substrate_host.go — the generic "status-substrate" F10 host-builder. The externalized
// `charly status` command plugin (candy/plugin-status) OWNS the CLI grammar + the PURE nested
// overlay + render + (K5) the declared-nested-tree pre-resolution (nested_tree.go, directly off
// the resolved-project envelope + deploykit.LoadBundleConfig — no host round-trip); it asks the
// host to do the one thing it still cannot do itself — collect the LIVE deployment state across
// every substrate (pod/vm/k8s/local/android), which needs the runtime-resolved engine/quadlet
// context + the per-substrate plugin fan-out (F11: "status-substrate" is a class-generic HostBuild
// kind, never a provider word).
const statusSubstrateBuilderKind = "status-substrate"

// hostBuildStatusSubstrate runs the status-collection engine host-side. req.Single selects the
// pod-scoped detail path (mirrors the former core status command's Collector.Single call);
// otherwise it runs the full multi-substrate fan-out (collectFlat). The declared nested tree is no
// longer resolved here (K5) — the candy computes its own roots and folds Rows+Roots itself.
func hostBuildStatusSubstrate(ctx context.Context, req spec.StatusSubstrateRequest, _ buildEngineContext) (spec.StatusSubstrateReply, error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return spec.StatusSubstrateReply{}, fmt.Errorf("status-substrate: resolve runtime: %w", err)
	}
	col, err := NewCollector(rt)
	if err != nil {
		return spec.StatusSubstrateReply{}, fmt.Errorf("status-substrate: new collector: %w", err)
	}

	if req.Single {
		ds, serr := col.Single(ctx, req.Box, req.Instance)
		if serr != nil {
			return spec.StatusSubstrateReply{}, fmt.Errorf("status-substrate: single: %w", serr)
		}
		return spec.StatusSubstrateReply{Single: ds}, nil
	}

	rows, _, ferr := col.collectFlat(ctx, req.IncludeAll)
	if ferr != nil {
		return spec.StatusSubstrateReply{}, fmt.Errorf("status-substrate: collect: %w", ferr)
	}
	return spec.StatusSubstrateReply{Rows: rows}, nil
}

var _ = func() bool {
	registerHostBuilder(statusSubstrateBuilderKind, typedHostBuilder(statusSubstrateBuilderKind, hostBuildStatusSubstrate))
	return true
}()
