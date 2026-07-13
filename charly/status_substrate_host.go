package main

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// status_substrate_host.go — the generic "status-substrate" F10 host-builder. The externalized
// `charly status` command plugin (candy/plugin-status) OWNS the CLI grammar + the PURE nested
// overlay + render; it asks the host to do the one thing it cannot do itself — collect the live
// deployment state across every substrate (pod/vm/k8s/local/android) and pre-resolve the DECLARED
// nested-deployment tree into the wire-safe spec.StatusNestedNode shape. Every core-coupled helper
// this needs (ResolveRuntime/NewCollector/Collector.collectFlat/Collector.Single/BundleNode/
// classifyTarget/ResolveDeployChain) stays in core — reached via this ONE generic action noun
// (F11: "status-substrate" is a class-generic HostBuild kind, never a provider word).
const statusSubstrateBuilderKind = "status-substrate"

// hostBuildStatusSubstrate runs the status-collection engine host-side. req.Single selects the
// pod-scoped detail path (mirrors the former core status command's Collector.Single call); otherwise it
// runs the full multi-substrate fan-out (collectFlat) and pre-resolves the declared nested tree
// (buildStatusRootsTree) — the candy's PURE overlay folds Rows+Roots without any core type.
func hostBuildStatusSubstrate(ctx context.Context, req spec.StatusSubstrateRequest, _ buildEngineContext) (spec.StatusSubstrateReply, error) {
	rt, err := ResolveRuntime()
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

	rows, opts, ferr := col.collectFlat(ctx, req.IncludeAll, req.Nested)
	if ferr != nil {
		return spec.StatusSubstrateReply{}, fmt.Errorf("status-substrate: collect: %w", ferr)
	}
	roots := buildStatusRootsTree(opts, req.Nested)
	return spec.StatusSubstrateReply{Rows: rows, Roots: roots}, nil
}

var _ = func() bool {
	registerHostBuilder(statusSubstrateBuilderKind, typedHostBuilder(statusSubstrateBuilderKind, hostBuildStatusSubstrate))
	return true
}()
