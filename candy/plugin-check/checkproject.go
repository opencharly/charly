package check

// checkproject.go — the AI-harness check-project PROJECTION, computed PLUGIN-SIDE off the
// resolved-project envelope (K5-U2/3, the check-config seam's death). The former host "check-config"
// HostBuild seam is GONE: the plugin fetches the generic resolved-project envelope
// (HostBuild("resolved-project")) and derives every field the harness leaves consume —
// bed-vs-iterate classification, the iterate sandbox class, the include-expanded scored plan, and
// the kind:agent catalog — directly from it. The ONE fact the envelope cannot carry (the per-host
// pod-overlay disposability) rides the thin retained "pod-disposable" host seam.
//
// The include-splicer (expandPlanIncludes) + the sandbox classifier (resolveIterateSandbox)
// relocated here from charly/check_include.go + charly/check_score_kind.go — they are pure over the
// envelope's Deploy tree / CandyModels / BoxPlans / Templates, so a plugin computes them with no
// core loader.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// checkProjection is the resolved check-project projection (the former spec.CheckConfigReply).
// IsBed/HasNode/HasIterate classify `charly check run <name>` into the deterministic bed path vs the
// AI iterate loop; SandboxKind/SandboxName are the iterate sandbox target ("pod"|"vm"|"host");
// PodTargetDisposable is the per-run pod-restart gate; IterateJSON/Plan/AgentBodies feed the iterate
// orchestration. Fields absent for a non-iterate entity / no-project stay zero.
type checkProjection struct {
	IsBed               bool
	HasNode             bool
	HasIterate          bool
	SandboxKind         string
	SandboxName         string
	PodTargetDisposable bool
	IterateJSON         json.RawMessage
	Plan                []spec.Step
	AgentBodies         map[string]json.RawMessage
}

// resolveCheckProjection resolves the check-project projection for entity (empty = catalog-only) off
// the resolved-project envelope. It replaces the former host check-project HostBuild seam: the plugin
// self-derives the projection from the generic envelope the host already serves.
func resolveCheckProjection(ex *sdk.Executor, ctx context.Context, entity, dir string) (checkProjection, error) {
	if ex == nil {
		return checkProjection{}, fmt.Errorf("charly check requires compiled-in placement (the resolved-project host seam is unavailable out-of-process)")
	}
	rp, err := resolvedProject(ex, ctx, dir)
	if err != nil {
		return checkProjection{}, err
	}

	proj := checkProjection{AgentBodies: rp.AgentBodies}
	if entity == "" {
		return proj, nil
	}

	// IsBed: a bed is a disposable, non-member deploy (the former uf.CheckBeds() predicate, pure
	// over the Deploy tree). An iterate entity IS also a bed — HasIterate is the discriminator.
	node, hasNode := rp.Deploy[entity]
	proj.HasNode = hasNode && node != nil
	proj.IsBed = proj.HasNode && node.IsDisposable() && node.MemberOf == ""
	proj.HasIterate = proj.HasNode && node.Iterate != nil
	if !proj.HasIterate {
		return proj, nil
	}

	// Iterate orchestration inputs — only for an iterate entity.
	tk, tn := resolveIterateSandbox(rp, node.Iterate.Sandbox)
	proj.SandboxKind = tk
	proj.SandboxName = tn
	if ij, mErr := json.Marshal(node.Iterate); mErr == nil {
		proj.IterateJSON = ij
	}
	// The per-run pod-restart gate: is the pod sandbox target disposable (per-host overlay)?
	if tk == targetKindPod {
		if disp, dErr := podDisposable(ex, ctx, tn); dErr == nil {
			proj.PodTargetDisposable = disp
		}
	}
	// The include-expanded scored plan (best-effort, matching the former seam's graceful degrade:
	// an unresolvable include leaves the plan empty rather than failing the whole projection).
	if plan, eErr := expandPlanIncludes(rp, node.Plan); eErr == nil {
		proj.Plan = plan
	}
	return proj, nil
}

// resolvedProject fetches + decodes the generic resolved-project envelope over the reverse channel.
func resolvedProject(ex *sdk.Executor, ctx context.Context, dir string) (*spec.ResolvedProject, error) {
	reqJSON, err := json.Marshal(spec.ResolvedProjectRequest{Dir: dir})
	if err != nil {
		return nil, err
	}
	out, err := ex.HostBuild(ctx, "resolved-project", reqJSON)
	if err != nil {
		return nil, err
	}
	var rp spec.ResolvedProject
	if err := json.Unmarshal(out, &rp); err != nil {
		return nil, fmt.Errorf("resolved-project: decode reply: %w", err)
	}
	return &rp, nil
}

// podDisposable resolves a per-host pod deploy overlay entry's disposability over the thin
// "pod-disposable" host seam (the ONE check-project fact the resolved-project envelope cannot carry).
func podDisposable(ex *sdk.Executor, ctx context.Context, name string) (bool, error) {
	reqJSON, err := json.Marshal(spec.PodDisposableRequest{Name: name})
	if err != nil {
		return false, err
	}
	out, err := ex.HostBuild(ctx, "pod-disposable", reqJSON)
	if err != nil {
		return false, err
	}
	var reply spec.PodDisposableReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return false, fmt.Errorf("pod-disposable: decode reply: %w", err)
	}
	return reply.Disposable, nil
}

// ---------------------------------------------------------------------------
// Iterate sandbox classification (relocated from charly/check_score_kind.go, rewritten over the
// envelope's Deploy tree). A bare sandbox ("") → host; an ssh-venue deploy → vm; a host-rooted
// deploy → host; everything else (the default container venue) → pod. The returned name is the
// sandbox deploy name (empty for a host sandbox).
// ---------------------------------------------------------------------------

func resolveIterateSandbox(rp *spec.ResolvedProject, sandbox string) (kind, name string) {
	if sandbox == "" {
		return targetKindHost, ""
	}
	if rp != nil {
		if node, ok := rp.Deploy[sandbox]; ok && node != nil {
			return targetKindForDeploy(node), sandbox
		}
	}
	return targetKindPod, sandbox
}

// targetKindForDeploy reads the deploy node's stamped descent traits (venue/host_rooted) — the SAME
// descriptor the core deploy chain branches on — instead of switching on the substrate kind word.
func targetKindForDeploy(node *spec.Deploy) string {
	d := node.Descent
	switch {
	case d != nil && d.Venue == "ssh":
		return targetKindVM
	case d != nil && d.HostRooted:
		return targetKindHost
	default:
		return targetKindPod
	}
}

// ---------------------------------------------------------------------------
// Include-splicer (relocated from charly/check_include.go, rewritten over the envelope). A
// `- include: <kind>:<name>` step splices the referenced entity's plan steps in place (recursively,
// cycle-safe). candy → rp.CandyModels[name].Plan; box → rp.BoxPlans[name] (host-flattened
// base-chain plan); pod/vm → the rp.Templates raw body decoded into spec.Pod/spec.Vm (a plugin MAY
// decode a concrete kind; the kernel may not).
// ---------------------------------------------------------------------------

// includeKinds enumerates the valid `<kind>` discriminator values.
var includeKinds = []string{"candy", "box", "pod", "vm"}

// expandPlanIncludes walks plan, replacing every `include:` step with the referenced entity's plan
// steps (recursively, cycle-safe). Non-include steps pass through unchanged.
func expandPlanIncludes(rp *spec.ResolvedProject, plan []spec.Step) ([]spec.Step, error) {
	return expandPlanIncludesInner(rp, plan, map[string]bool{})
}

func expandPlanIncludesInner(rp *spec.ResolvedProject, plan []spec.Step, visited map[string]bool) ([]spec.Step, error) {
	var out []spec.Step
	for _, s := range plan {
		if !s.IsInclude() {
			out = append(out, s)
			continue
		}
		ref := strings.TrimSpace(s.Include)
		if visited[ref] {
			return nil, fmt.Errorf("include cycle detected at %q", ref)
		}
		kind, name, err := splitIncludeRef(ref)
		if err != nil {
			return nil, err
		}
		steps, err := collectIncludeSteps(rp, kind, name)
		if err != nil {
			return nil, fmt.Errorf("include %q: %w", ref, err)
		}
		// Stamp the source origin for reporting when not already set.
		origin := kind + ":" + name
		for i := range steps {
			if steps[i].Origin == "" {
				steps[i].Origin = origin
			}
		}
		visited[ref] = true
		expanded, err := expandPlanIncludesInner(rp, steps, visited)
		delete(visited, ref)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

// splitIncludeRef splits a `<kind>:<name>` include directive into its kind and name, validating the
// kind against includeKinds.
func splitIncludeRef(ref string) (kind, name string, err error) {
	before, after, ok := strings.Cut(ref, ":")
	if !ok {
		return "", "", fmt.Errorf("include %q: expected <kind>:<name> (one of: %s)", ref, strings.Join(includeKinds, ", "))
	}
	kind = strings.TrimSpace(before)
	name = strings.TrimSpace(after)
	if name == "" {
		return "", "", fmt.Errorf("include %q: missing entity name after kind %q", ref, kind)
	}
	if slices.Contains(includeKinds, kind) {
		return kind, name, nil
	}
	return "", "", fmt.Errorf("include %q: invalid kind %q (one of: %s)", ref, kind, strings.Join(includeKinds, ", "))
}

// collectIncludeSteps returns the referenced entity's plan steps off the envelope.
func collectIncludeSteps(rp *spec.ResolvedProject, kind, name string) ([]spec.Step, error) {
	switch kind {
	case "candy":
		m, ok := rp.CandyModels[name]
		if !ok {
			return nil, fmt.Errorf("candy %q not found (available: %s)", name, sortedMapKeys(rp.CandyModels))
		}
		return append([]spec.Step(nil), m.Plan...), nil

	case "box":
		// rp.BoxPlans carries the host-flattened base-chain acceptance plan per box (candy-chain
		// bakeable steps + the box-level bakeable plan), keyed by QUALIFIED name — the exact result
		// the former in-core box arm produced via CollectDescriptions. A box with no plan (or a
		// missing box) is absent from the map.
		steps, ok := rp.BoxPlans[name]
		if !ok {
			return nil, fmt.Errorf("box %q not found or produced no plan (available: %s)", name, sortedMapKeys(rp.BoxPlans))
		}
		return append([]spec.Step(nil), steps...), nil

	case "pod":
		raw, ok := templateBody(rp, "pod", name)
		if !ok {
			return nil, fmt.Errorf("pod %q not found (available: %s)", name, sortedTemplateKeys(rp, "pod"))
		}
		var pod spec.Pod
		if err := json.Unmarshal(raw, &pod); err != nil {
			return nil, fmt.Errorf("pod %q decode: %w", name, err)
		}
		return append([]spec.Step(nil), pod.Plan...), nil

	case "vm":
		raw, ok := templateBody(rp, "vm", name)
		if !ok {
			return nil, fmt.Errorf("vm %q not found (available: %s)", name, sortedTemplateKeys(rp, "vm"))
		}
		var vm spec.Vm
		if err := json.Unmarshal(raw, &vm); err != nil {
			return nil, fmt.Errorf("vm %q decode: %w", name, err)
		}
		return append([]spec.Step(nil), vm.Plan...), nil
	}
	return nil, fmt.Errorf("unhandled include kind %q", kind)
}

// templateBody returns the raw pod:/vm: template body from the envelope's opaque template maps.
func templateBody(rp *spec.ResolvedProject, kind, name string) (json.RawMessage, bool) {
	if rp == nil || rp.Templates == nil {
		return nil, false
	}
	var m map[string]spec.RawBody
	switch kind {
	case "pod":
		m = rp.Templates.Pod
	case "vm":
		m = rp.Templates.VM
	}
	raw, ok := m[name]
	return raw, ok
}

func sortedTemplateKeys(rp *spec.ResolvedProject, kind string) string {
	if rp == nil || rp.Templates == nil {
		return ""
	}
	switch kind {
	case "pod":
		return sortedMapKeys(rp.Templates.Pod)
	case "vm":
		return sortedMapKeys(rp.Templates.VM)
	}
	return ""
}

// sortedMapKeys returns the map's keys sorted + comma-joined (for a friendly not-found hint).
func sortedMapKeys[T any](m map[string]T) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
