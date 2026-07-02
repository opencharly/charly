package main

import (
	"context"
	"sort"
	"time"
)

// status_nested.go — the NESTED-substrate overlay for `charly status`.
//
// Unlike the pod / vm / k8s / local / android collectors (each a
// SubstrateCollector that contributes flat rows), the nested overlay does NOT
// register a collector. There is no "nested backend" to probe in isolation — a
// nested child's venue is always REACHED THROUGH its parent (pod→android,
// vm→pod, vm→host, …). So instead of a registered collector this file
// post-processes the already-merged flat rows: it reads the DECLARED nested
// tree from the deploy config, attaches each declared child to its parent
// row's DeploymentStatus.Nested[], and — under --nested — probes each child's
// live venue through the same ResolveDeployChain + NestedExecutor primitive
// `charly bundle add` / `charly check live parent.child` use.
//
// Collector.All calls applyNestedOverlay exactly once, after the substrate
// fan-out and before the final sort.

// nestedProbeTimeout bounds the per-child live probe under --nested. A child
// whose multi-hop venue doesn't answer within this window renders
// Status:"unreachable" instead of blocking the whole table. This is a context
// DEADLINE, never a sleep/retry loop (CLAUDE.md R4) — the deadline cancels the
// in-flight RunCapture and the row falls through to "unreachable".
const nestedProbeTimeout = 4 * time.Second

// applyNestedOverlay folds nested children into their parent rows for the
// `charly status` table/JSON/detail output — and DEDUPLICATES: a declared nested
// child that ALSO surfaced as a flat top-level row (an AndroidCollector row at
// the dotted path, or a nested-pod row at the flattened container name) is
// MOVED under its parent and REMOVED from the top level, so it appears exactly
// once.
//
// Default (opts.Nested == false): attach the DECLARED nested structure. A child
// with a matching flat row inherits that flat row's REAL collected data
// (status / uptime / container / ports / devices / tools / volumes / source);
// a child with no flat match renders the synthesized declared row (Source
// "nested"). No multi-hop work, no extra subprocesses.
//
// Under opts.Nested: probe each declared child's LIVE venue via
// ResolveDeployChain + the resulting DeployExecutor, under a STRICT per-child
// context timeout. A timed-out or failing child renders Status:"unreachable";
// the table is NEVER blocked (deadline-only, no sleep loops — R4).
func applyNestedOverlay(rows []DeploymentStatus, opts CollectOpts) []DeploymentStatus {
	roots := mergedNestedRoots(opts)
	if len(roots) == 0 {
		return rows
	}

	// Index the flat rows by deploy key so a declared parent finds its
	// already-collected row, and a declared child can claim its own flat
	// row (a nested pod is also a top-level charly-<flat-path> container the pod
	// collector saw; a nested android device is a flat AndroidCollector row
	// keyed on its dotted path).
	byKey := make(map[string]int, len(rows))
	for i := range rows {
		byKey[deployKey(rows[i].Image, rows[i].Instance)] = i
	}

	// claimed records the flat-row indices that have been MOVED into a nested
	// position. They are dropped from the top-level slice at the end so a
	// declared nested child is never double-counted.
	claimed := make(map[int]bool)

	for _, name := range sortedRootKeys(roots) {
		root := roots[name]
		if !root.HasChildren() {
			continue
		}
		pi, ok := byKey[name]
		if !ok {
			// The declared parent has no flat row (not running, not in --all).
			// Nothing to attach to — skip rather than synthesize a phantom
			// parent row, which would double-count an absent deploy.
			continue
		}
		rows[pi].Nested = buildNestedChildren(name, &root, rows, byKey, roots, claimed, opts)
	}

	// Drop every claimed flat row from the top level — it now lives under its
	// parent's Nested[]. Preserve order for the remaining rows.
	if len(claimed) == 0 {
		return rows
	}
	kept := rows[:0]
	for i := range rows {
		if claimed[i] {
			continue
		}
		kept = append(kept, rows[i])
	}
	return kept
}

// buildNestedChildren renders the direct nested children of parentNode (at
// dotted path parentPath) as DeploymentStatus rows, recursing into deeper
// nesting. Children are emitted in sorted key order for stable output. A child
// that claims a flat row adds that row's index to claimed.
func buildNestedChildren(parentPath string, parentNode *BundleNode, rows []DeploymentStatus, byKey map[string]int, roots map[string]BundleNode, claimed map[int]bool, opts CollectOpts) []DeploymentStatus {
	if !parentNode.HasChildren() {
		return nil
	}
	keys := make([]string, 0, len(parentNode.Children))
	for k := range parentNode.Children {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]DeploymentStatus, 0, len(keys))
	for _, k := range keys {
		child := parentNode.Children[k]
		if child == nil {
			continue
		}
		childPath := parentPath + "." + k
		cs := nestedChildStatus(childPath, k, child, rows, byKey, roots, claimed, opts)
		cs.Nested = buildNestedChildren(childPath, child, rows, byKey, roots, claimed, opts)
		out = append(out, cs)
	}
	return out
}

// nestedChildStatus builds one nested child's DeploymentStatus. The Image cell
// shows the declared child key; Kind comes from the node's target.
//
// If the child has a MATCHING flat row — a flat AndroidCollector row keyed on
// the dotted childPath, OR a nested-pod row keyed on NestedContainerName
// (the flattened charly-<seg1_seg2> name) — that flat row's REAL collected data is
// MOVED into the nested position (status / uptime / container / ports /
// devices / tools / volumes, preserving its real Source like "adb"/"podman")
// and its index recorded in claimed so applyNestedOverlay drops it from the
// top level. A child with NO flat match keeps the synthesized "declared" row
// with Source "nested". Under --nested the status is then refined by a live
// multi-hop probe.
func nestedChildStatus(childPath, childKey string, child *BundleNode, rows []DeploymentStatus, byKey map[string]int, roots map[string]BundleNode, claimed map[int]bool, opts CollectOpts) DeploymentStatus {
	cs := DeploymentStatus{
		Kind:    nestedChildKind(child),
		Image:   childKey,
		Status:  "declared",
		RunMode: opts.RunMode,
		Source:  "nested",
	}

	// A declared nested child that ALSO surfaced flat (AndroidCollector row at
	// the dotted path, or nested-pod row at the flattened container name) is
	// MOVED here: inherit its real data and claim its index for removal. The
	// dotted-path key is tried first (android), then the flattened name (pod).
	if flatRow, ok := claimFlatRow(childPath, byKey, claimed); ok {
		src := rows[flatRow]
		cs.Status = src.Status
		cs.Uptime = src.Uptime
		cs.Container = src.Container
		cs.Ports = src.Ports
		cs.Devices = src.Devices
		cs.Tools = src.Tools
		cs.Volumes = src.Volumes
		cs.Network = src.Network
		cs.Tunnel = src.Tunnel
		// Preserve the flat row's real provenance (adb/podman/...), not the
		// synthesized "nested" stamp — the data really came from that
		// substrate's live collection.
		cs.Source = src.Source
		claimed[flatRow] = true
	}

	if !opts.Nested {
		return cs
	}

	// --nested: probe the child's live venue through the real multi-hop
	// chain, bounded by a strict per-child deadline.
	cs.Status = probeNestedChildLive(childPath, roots)
	return cs
}

// claimFlatRow finds the flat-row index that a declared nested child at
// childPath corresponds to, if any: the dotted-path key first (the shape
// AndroidCollector rows carry) then NestedContainerName (the flattened
// charly-<seg1_seg2> name a nested pod carries). A row already claimed by a
// different parent is not returned twice.
func claimFlatRow(childPath string, byKey map[string]int, claimed map[int]bool) (int, bool) {
	for _, key := range []string{childPath, NestedContainerName(childPath)} {
		if i, ok := byKey[key]; ok && !claimed[i] {
			return i, true
		}
	}
	return 0, false
}

// probeNestedChildLive resolves the dotted path to a DeployExecutor chain and
// runs a trivial liveness probe under nestedProbeTimeout. Returns "reachable"
// on a clean exit, "unreachable" on any error / non-zero exit / timeout. The
// chain construction reuses ResolveDeployChain — the SAME primitive `charly bundle
// add` and `charly check live parent.child` use (R3); there is no bespoke nested
// dial here.
func probeNestedChildLive(childPath string, roots map[string]BundleNode) string {
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
func nestedChildKind(child *BundleNode) SubstrateKind {
	switch classifyTarget(child) {
	case "vm":
		return SubstrateVM
	case "k8s":
		return SubstrateK8s
	case "local", "host":
		return SubstrateLocal
	case "android":
		return SubstrateAndroid
	default:
		return SubstratePod
	}
}

// mergedNestedRoots returns the declared deployment tree (project +
// per-machine); check beds are `disposable: true` bundles already in the project
// Bundle map. Mirrors resolveTreeRoot's merge precedence
// (project then local overlay) but operates on the ALREADY-LOADED configs in
// opts — applyNestedOverlay must not re-read disk or re-run LoadUnified.
func mergedNestedRoots(opts CollectOpts) map[string]BundleNode {
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
// overlay attaches children in stable order across runs.
func sortedRootKeys(roots map[string]BundleNode) []string {
	keys := make([]string, 0, len(roots))
	for k := range roots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
