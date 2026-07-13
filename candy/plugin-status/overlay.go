package status

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// overlay.go — the PURE nested-deployment overlay for `charly status`, ported verbatim (same
// semantics, new data shape) from the former charly/status_nested.go applyNestedOverlay /
// buildNestedChildren / nestedChildStatus / claimFlatRow. It operates ENTIRELY on the wire-safe
// []spec.StatusNestedNode tree the host pre-resolves (buildStatusRootsTree,
// charly/status_nested.go) — NO core types, NO ResolveDeployChain, NO classifyTarget: every
// kind/match-key/live-status decision was already made host-side.
//
// Preserved EXACTLY:
//   - claim → inherit the flat row's real data + provenance → drop the claimed row from the top
//     level (dedup), so a declared nested child that ALSO surfaced as its own flat top-level row
//     (a nested-pod's flattened charly-<seg1_seg2> container, or an AndroidCollector row at its
//     dotted path) appears exactly once, under its parent.
//   - synthesize a "declared"/Source="nested" row when no flat match exists.
//   - a declared parent with NO flat row attaches nothing (no phantom parent row).
//   - children emit in the HOST's sorted key order (buildStatusChildNodes already sorted them;
//     this file never re-sorts).
//   - under --nested, EVERY child's Status is overwritten by its live-probe verdict
//     ("reachable"/"unreachable"), even a claimed (already-real) child — mirrors the former
//     nestedChildStatus's unconditional `cs.Status = probeNestedChildLive(...)` under
//     opts.Nested. The host only populates LiveStatus when nested was requested
//     (probeNestedChildLive always returns a non-empty verdict), so `LiveStatus != ""` is the
//     exact candy-side equivalent of the former `opts.Nested` check.
//   - RunMode: the former code stamped every synthesized/claimed nested row with the single
//     opts.RunMode value (uniform across one `charly status` invocation, never overwritten by a
//     claim). Since every flat row in one reply carries that SAME opts.RunMode (every substrate
//     collector stamps it), this file derives the equivalent value from the ROOT's own flat row
//     (rows[pi].RunMode) and threads it through the whole recursion — byte-identical output with
//     no extra wire field.

// applyNestedOverlay folds the host-pre-resolved nested tree into the parent rows for the
// `charly status` table/JSON/detail output, deduplicating claimed flat rows out of the top level.
func applyNestedOverlay(rows []spec.DeploymentStatus, roots []spec.StatusNestedNode) []spec.DeploymentStatus {
	if len(roots) == 0 {
		return rows
	}

	// Index the flat rows by deploy key so a declared parent finds its already-collected row,
	// and a declared child can claim its own flat row.
	byKey := make(map[string]int, len(rows))
	for i := range rows {
		byKey[deploykit.DeployKey(rows[i].Image, rows[i].Instance)] = i
	}

	// claimed records the flat-row indices that have been MOVED into a nested position. They are
	// dropped from the top-level slice at the end so a declared nested child is never
	// double-counted.
	claimed := make(map[int]bool)

	for i := range roots {
		root := &roots[i]
		if !root.HasChildren || len(root.MatchKeys) == 0 {
			continue
		}
		pi, ok := byKey[root.MatchKeys[0]]
		if !ok {
			// The declared parent has no flat row (not running, not in --all). Nothing to
			// attach to — skip rather than synthesize a phantom parent row, which would
			// double-count an absent deploy.
			continue
		}
		rows[pi].Nested = buildNestedChildren(root.Children, rows, byKey, claimed, rows[pi].RunMode)
	}

	// Drop every claimed flat row from the top level — it now lives under its parent's
	// Nested[]. Preserve order for the remaining rows.
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

// buildNestedChildren renders the direct nested children of a pre-resolved node's Children as
// DeploymentStatus rows, recursing into deeper nesting. Children are already in the host's sorted
// key order — this file never re-sorts. A child that claims a flat row adds that row's index to
// claimed.
func buildNestedChildren(children []*spec.StatusNestedNode, rows []spec.DeploymentStatus, byKey map[string]int, claimed map[int]bool, runMode string) []*spec.DeploymentStatus {
	if len(children) == 0 {
		return nil
	}
	out := make([]*spec.DeploymentStatus, 0, len(children))
	for _, child := range children {
		if child == nil {
			continue
		}
		cs := nestedChildStatus(child, rows, byKey, claimed, runMode)
		cs.Nested = buildNestedChildren(child.Children, rows, byKey, claimed, runMode)
		out = append(out, &cs)
	}
	return out
}

// nestedChildStatus builds one nested child's DeploymentStatus. The Image cell shows the declared
// child key; Kind is the host-resolved substrate kind.
//
// If the child has a MATCHING flat row (its MatchKeys, tried in the host's priority order — the
// dotted path first, then the flattened nested-container name), that flat row's REAL collected
// data is MOVED into the nested position (status / uptime / container / ports / devices / tools /
// volumes / network / tunnel, preserving its real Source like "adb"/"podman") and its index
// recorded in claimed so applyNestedOverlay drops it from the top level. A child with NO flat
// match keeps the synthesized "declared" row with Source "nested". Under --nested (LiveStatus
// non-empty), the live probe verdict OVERWRITES Status unconditionally — even a just-claimed
// child.
func nestedChildStatus(child *spec.StatusNestedNode, rows []spec.DeploymentStatus, byKey map[string]int, claimed map[int]bool, runMode string) spec.DeploymentStatus {
	cs := spec.DeploymentStatus{
		Kind:    child.Kind,
		Image:   child.Key,
		Status:  "declared",
		RunMode: runMode,
		Source:  "nested",
	}

	if flatRow, ok := claimFlatRow(child.MatchKeys, byKey, claimed); ok {
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
		// Preserve the flat row's real provenance (adb/podman/...), not the synthesized
		// "nested" stamp — the data really came from that substrate's live collection.
		cs.Source = src.Source
		claimed[flatRow] = true
	}

	if child.LiveStatus != "" {
		cs.Status = child.LiveStatus
	}
	return cs
}

// claimFlatRow finds the flat-row index that a declared nested child corresponds to, if any,
// trying each of matchKeys IN ORDER (the host's priority: the dotted path first, then the
// flattened nested-container name). A row already claimed by a different parent is not returned
// twice.
func claimFlatRow(matchKeys []string, byKey map[string]int, claimed map[int]bool) (int, bool) {
	for _, key := range matchKeys {
		if i, ok := byKey[key]; ok && !claimed[i] {
			return i, true
		}
	}
	return 0, false
}
