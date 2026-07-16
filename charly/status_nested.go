package main

import (
	"context"
	"sort"
	"time"

	"github.com/opencharly/sdk/spec"
)

// status_nested.go — the HOST tree-builder for `charly status`'s nested overlay.
//
// The PURE fold (claim a declared child's flat row → inherit its real data →
// drop it from the top level; synthesize a "declared"/Source="nested" row when
// no flat match exists; live-probe under --nested) moved to the command:status
// candy's overlay.go, operating on the wire-safe spec.StatusNestedNode tree
// this file pre-resolves. Everything here is HOST-COUPLED — it reads the
// project's declared deploy tree (BundleNode / classifyTarget /
// ResolveDeployChain), which a plugin cannot decode or dial — so it stays
// core, reached ONLY via the "status-substrate" HostBuild seam
// (status_substrate_host.go).
//
// buildStatusRootsTree is called once per `charly status` (non-single) run,
// AFTER Collector.collectFlat and BEFORE the wire reply is returned to the
// candy.

// nestedProbeTimeout bounds the per-child live probe under --nested. A child
// whose multi-hop venue doesn't answer within this window renders
// Status:"unreachable" instead of blocking the whole table. This is a context
// DEADLINE, never a sleep/retry loop (CLAUDE.md R4) — the deadline cancels the
// in-flight RunCapture and the row falls through to "unreachable".
const nestedProbeTimeout = 4 * time.Second

// buildStatusRootsTree pre-resolves the DECLARED nested-deployment tree into
// the wire-safe []spec.StatusNestedNode shape the command:status candy's PURE
// overlay folds — every core-coupled decision (which kind a node is, which
// flat-row keys index it, and — under nested — its live-probe verdict) is made
// HERE, so the candy needs no core type, no ResolveDeployChain, no
// classifyTarget. Only roots WITH children are emitted (the candy's pure
// overlay skips a childless root anyway — nothing to attach).
func buildStatusRootsTree(opts CollectOpts, nested bool) []spec.StatusNestedNode {
	rawRoots := mergedNestedRoots(opts)
	if len(rawRoots) == 0 {
		return nil
	}
	var out []spec.StatusNestedNode
	for _, key := range sortedRootKeys(rawRoots) {
		root := rawRoots[key]
		if !root.HasChildren() {
			continue
		}
		out = append(out, spec.StatusNestedNode{
			Key:         key,
			Path:        key,
			Kind:        nestedChildKind(&root),
			HasChildren: true,
			MatchKeys:   []string{key},
			Children:    buildStatusChildNodes(key, &root, rawRoots, nested),
		})
	}
	return out
}

// buildStatusChildNodes recurses buildStatusRootsTree's per-root walk into the
// declared children of parentNode (at dotted path parentPath), mirroring the
// former buildNestedChildren's structure + sorted key order but emitting the
// wire-safe spec.StatusNestedNode instead of a live *DeploymentStatus. Each
// child's MatchKeys carries BOTH candidate flat-row keys in the SAME priority
// order the former claimFlatRow tried them (dotted path first, then the
// flattened NestedContainerName) so the candy's claim logic needs no core
// knowledge of which shape wins.
func buildStatusChildNodes(parentPath string, parentNode *spec.BundleNode, rawRoots map[string]spec.BundleNode, nested bool) []*spec.StatusNestedNode {
	if !parentNode.HasChildren() {
		return nil
	}
	keys := make([]string, 0, len(parentNode.Children))
	for k := range parentNode.Children {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]*spec.StatusNestedNode, 0, len(keys))
	for _, k := range keys {
		child := parentNode.Children[k]
		if child == nil {
			continue
		}
		childPath := parentPath + "." + k
		node := &spec.StatusNestedNode{
			Key:         k,
			Path:        childPath,
			Kind:        nestedChildKind(child),
			HasChildren: child.HasChildren(),
			MatchKeys:   []string{childPath, NestedContainerName(childPath)},
			Children:    buildStatusChildNodes(childPath, child, rawRoots, nested),
		}
		if nested {
			node.LiveStatus = probeNestedChildLive(childPath, rawRoots)
		}
		out = append(out, node)
	}
	return out
}

// probeNestedChildLive resolves the dotted path to a DeployExecutor chain and
// runs a trivial liveness probe under nestedProbeTimeout. Returns "reachable"
// on a clean exit, "unreachable" on any error / non-zero exit / timeout. The
// chain construction reuses ResolveDeployChain — the SAME primitive `charly bundle
// add` and `charly check live parent.child` use (R3); there is no bespoke nested
// dial here.
func probeNestedChildLive(childPath string, roots map[string]spec.BundleNode) string {
	leaf, chain, err := ResolveDeployChain(roots, childPath, nil)
	if err != nil || chain == nil || leaf == nil {
		return "unreachable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), nestedProbeTimeout)
	defer cancel()
	_, _, exit, perr := chain.RunCapture(ctx, "true")
	if perr != nil || exit != 0 {
		return "unreachable"
	}
	return "reachable"
}

// nestedChildKind maps a nested node's target to the SubstrateKind used for
// the row's KIND cell. classifyTarget normalizes empty/legacy spellings, so
// pod / vm / k8s / local / android all resolve to their canonical kind.
func nestedChildKind(child *spec.BundleNode) spec.SubstrateKind {
	switch classifyTarget(child) {
	case "vm":
		return spec.SubstrateVM
	case "k8s":
		return spec.SubstrateK8s
	case "local", "host":
		return spec.SubstrateLocal
	case "android":
		return spec.SubstrateAndroid
	default:
		return spec.SubstratePod
	}
}

// mergedNestedRoots returns the declared deployment tree (project +
// per-machine); check beds are `disposable: true` bundles already in the project
// Bundle map. Mirrors resolveTreeRoot's merge precedence
// (project then local overlay) but operates on the ALREADY-LOADED configs in
// opts — buildStatusRootsTree must not re-read disk or re-run LoadUnified.
func mergedNestedRoots(opts CollectOpts) map[string]spec.BundleNode {
	var project *BundleConfig
	if opts.Unified != nil {
		project = opts.Unified.ProjectBundleConfig()
	}
	merged := MergeDeployConfigs(project, opts.Deploy)
	if merged == nil {
		return nil
	}
	return merged.Bundle
}

// sortedRootKeys returns deploy-tree root keys in deterministic order so the
// tree-builder walks children in stable order across runs.
func sortedRootKeys(roots map[string]spec.BundleNode) []string {
	keys := make([]string, 0, len(roots))
	for k := range roots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
