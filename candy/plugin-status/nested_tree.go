package status

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// nested_tree.go — the DECLARED nested-deployment tree pre-resolution, ported from
// charly/status_nested.go (K5). The former host header claimed this was CORE-COUPLED
// ("BundleConfig/ResolveDeployChain/NestedExecutor... a plugin cannot decode or dial") —
// that claim was STALE relative to what candy/plugin-substrate's own android status collector
// already proved: deploykit.LoadBundleConfig/MergeDeployConfigs/ClassifyTarget/ResolveDeployChain
// are ALL sdk-portable, so a plugin decodes + dials them exactly like the host did. The ONLY thing
// that genuinely could not cross the process boundary was never true — it was never attempted.
//
// buildStatusRootsTree resolves the declared tree (project, via HostBuild("resolved-project"),
// merged with the operator's per-host overlay via deploykit.LoadBundleConfig) into the wire-safe
// []spec.StatusNestedNode shape overlay.go's PURE fold (applyNestedOverlay) consumes.

// nestedProbeTimeout bounds the per-child live probe under --nested. A child whose multi-hop venue
// doesn't answer within this window renders Status:"unreachable" instead of blocking the whole
// table. A context DEADLINE, never a sleep/retry loop (CLAUDE.md R4).
const nestedProbeTimeout = 4 * time.Second

// resolvedProject fetches the resolved-project envelope over the reverse channel — the same
// HostBuild("resolved-project") seam candy/plugin-check and candy/plugin-substrate already consume.
func resolvedProject(ex *sdk.Executor, ctx context.Context) (*spec.ResolvedProject, error) {
	reqJSON, err := json.Marshal(spec.ResolvedProjectRequest{Dir: ""})
	if err != nil {
		return nil, err
	}
	out, err := ex.HostBuild(ctx, "resolved-project", reqJSON)
	if err != nil {
		return nil, err
	}
	var rp spec.ResolvedProject
	if uerr := json.Unmarshal(out, &rp); uerr != nil {
		return nil, uerr
	}
	return &rp, nil
}

// buildStatusRootsTree pre-resolves the DECLARED nested-deployment tree into the wire-safe
// []spec.StatusNestedNode shape the PURE overlay folds. It is a thin I/O wrapper: fetch the
// merged roots (the only part that needs ex/ctx), then hand off to the PURE, directly-testable
// buildStatusRootsTreeFrom (mirrors candy/plugin-substrate's collectAndroidDeployNodes split —
// I/O in the outer function, a plain-parameter pure function underneath, so a test never touches
// deploykit.LoadBundleConfig()'s real host file, R3).
func buildStatusRootsTree(ex *sdk.Executor, ctx context.Context, nested bool) ([]spec.StatusNestedNode, error) {
	rawRoots, err := mergedNestedRoots(ex, ctx)
	if err != nil {
		return nil, err
	}
	return buildStatusRootsTreeFrom(rawRoots, nested), nil
}

// buildStatusRootsTreeFrom is the PURE tree-assembly step: every decision (which kind a node is,
// which flat-row keys index it, and — under nested — its live-probe verdict) is made HERE, given
// an already-merged roots map. Only roots WITH children are emitted (the pure overlay skips a
// childless root anyway).
func buildStatusRootsTreeFrom(rawRoots map[string]deploykit.BundleNode, nested bool) []spec.StatusNestedNode {
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

// buildStatusChildNodes recurses buildStatusRootsTree's per-root walk into the declared children
// of parentNode (at dotted path parentPath). Each child's MatchKeys carries BOTH candidate flat-row
// keys in the SAME priority order the pure overlay's claimFlatRow tries them (dotted path first,
// then the flattened NestedContainerName).
func buildStatusChildNodes(parentPath string, parentNode *deploykit.BundleNode, rawRoots map[string]deploykit.BundleNode, nested bool) []*spec.StatusNestedNode {
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
			MatchKeys:   []string{childPath, kit.NestedContainerName(childPath)},
			Children:    buildStatusChildNodes(childPath, child, rawRoots, nested),
		}
		if nested {
			node.LiveStatus = probeNestedChildLive(childPath, rawRoots)
		}
		out = append(out, node)
	}
	return out
}

// probeNestedChildLive resolves the dotted path to a DeployExecutor chain and runs a trivial
// liveness probe under nestedProbeTimeout. Returns "reachable" on a clean exit, "unreachable" on
// any error / non-zero exit / timeout. The chain construction reuses deploykit.ResolveDeployChain —
// the SAME primitive `charly bundle add` and `charly check live parent.child` use (R3).
func probeNestedChildLive(childPath string, roots map[string]deploykit.BundleNode) string {
	leaf, chain, err := deploykit.ResolveDeployChain(roots, childPath, nil)
	if err != nil || chain == nil || leaf == nil {
		return "unreachable"
	}
	pctx, cancel := context.WithTimeout(context.Background(), nestedProbeTimeout)
	defer cancel()
	_, _, exit, perr := chain.RunCapture(pctx, "true")
	if perr != nil || exit != 0 {
		return "unreachable"
	}
	return "reachable"
}

// nestedChildKind maps a nested node's target to the SubstrateKind used for the row's KIND cell.
// deploykit.ClassifyTarget normalizes empty/legacy spellings, so pod/vm/k8s/local/android all
// resolve to their canonical kind.
func nestedChildKind(child *deploykit.BundleNode) spec.SubstrateKind {
	switch deploykit.ClassifyTarget(child) {
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

// mergedNestedRoots returns the declared deployment tree (project + per-machine overlay) — the
// I/O half: fetch the project envelope + the operator's per-host overlay, then hand off to the
// PURE mergedNestedRootsFrom. Mirrors candy/plugin-substrate's status_android_collect.go split
// (fetchResolvedProject + deploykit.LoadBundleConfig() in the outer function,
// collectAndroidDeployNodes as the pure plain-parameter function) exactly, R3.
func mergedNestedRoots(ex *sdk.Executor, ctx context.Context) (map[string]deploykit.BundleNode, error) {
	rp, err := resolvedProject(ex, ctx)
	if err != nil {
		return nil, err
	}
	// Best-effort: absence of a per-machine overlay is normal (mirrors
	// candy/plugin-substrate's newFlatCollector, K6, the same pattern).
	perMachine, _ := deploykit.LoadBundleConfig()
	return mergedNestedRootsFrom(rp, perMachine), nil
}

// mergedNestedRootsFrom is the PURE merge step: project deploy tree (project then local overlay
// wins per key, deploykit.MergeDeployConfigs — the SAME precedence resolveTreeRoot uses),
// callable directly from a test with in-memory fixtures (no LoadBundleConfig I/O).
func mergedNestedRootsFrom(rp *spec.ResolvedProject, perMachine *deploykit.BundleConfig) map[string]deploykit.BundleNode {
	projectBundle := make(map[string]deploykit.BundleNode, len(rp.Deploy))
	for name, node := range rp.Deploy {
		if node != nil {
			projectBundle[name] = deploykit.BundleNode(*node)
		}
	}
	merged := deploykit.MergeDeployConfigs(&deploykit.BundleConfig{Bundle: projectBundle}, perMachine)
	if merged == nil {
		return nil
	}
	return merged.Bundle
}

// sortedRootKeys returns deploy-tree root keys in deterministic order so the tree-builder walks
// children in stable order across runs.
func sortedRootKeys(roots map[string]deploykit.BundleNode) []string {
	keys := make([]string, 0, len(roots))
	for k := range roots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
