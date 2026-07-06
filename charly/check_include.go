package main

// check_include.go — the `include: <kind>:<name>` plan-composition primitive
// (the cross-entity composition directive; the former `from:` block is gone).
//
// A `- include: <kind>:<name>` step splices the referenced entity's plan steps
// in place at collect/bake time, so the runner + scorer see a flat plan with
// no include directives. Selection that the old `select:`/`exclude:` did is now
// tag/id-based: authors filter via step `tag:` (the runner's --tag/Filter).
// Cycle-safe (visited set); spliced steps carry their source origin for
// reporting.

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// includeKinds enumerates the valid `<kind>` discriminator values.
var includeKinds = []string{"candy", "box", "pod", "vm"}

// ExpandPlanIncludes walks plan, replacing every `include:` step with the
// referenced entity's plan steps (recursively, cycle-safe). Non-include steps
// pass through unchanged.
func ExpandPlanIncludes(uf *UnifiedFile, layers map[string]*Candy, plan []Step) ([]Step, error) {
	return expandPlanIncludes(uf, layers, plan, map[string]bool{})
}

func expandPlanIncludes(uf *UnifiedFile, layers map[string]*Candy, plan []Step, visited map[string]bool) ([]Step, error) {
	var out []Step
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
		steps, err := collectIncludeSteps(uf, layers, kind, name)
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
		expanded, err := expandPlanIncludes(uf, layers, steps, visited)
		delete(visited, ref)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

// splitIncludeRef splits a `<kind>:<name>` include directive into its kind and
// name, validating the kind against includeKinds.
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

// collectIncludeSteps returns the referenced entity's plan steps. Candy/pod/vm
// read the entity's own plan; box walks the candy chain via CollectDescriptions
// and flattens the three sections.
func collectIncludeSteps(uf *UnifiedFile, layers map[string]*Candy, kind, name string) ([]Step, error) {
	switch kind {
	case "candy":
		layer, ok := layers[name]
		if !ok {
			return nil, fmt.Errorf("candy %q not found (available: %s)", name, sortedCandyNames(layers))
		}
		return append([]Step(nil), layer.plan...), nil

	case "box":
		// Namespace-aware: a box may live in an imported submodule.
		cfg := uf.ProjectConfig()
		_, nsCfg, ok := cfg.resolveBoxRef(name)
		if !ok {
			return nil, fmt.Errorf("box %q not found (available: %s)", name, sortedBoxKeys(uf))
		}
		set := CollectDescriptions(nsCfg, layers, leafName(name))
		if set == nil {
			return nil, fmt.Errorf("box %q produced no plan (no candy in the chain has a plan: list)", name)
		}
		var out []Step
		for _, sec := range [][]LabeledDescription{set.Candy, set.Box, set.Deploy} {
			for _, ld := range sec {
				out = append(out, ld.Plan...)
			}
		}
		return out, nil

	case "pod":
		body, ok := uf.Pod[name]
		if !ok {
			return nil, fmt.Errorf("pod %q not found (available: %s)", name, sortedPodKeys(uf))
		}
		pod, perr := resolvePodViaPlugin(body)
		if perr != nil || pod == nil {
			return nil, fmt.Errorf("pod %q resolve: %w", name, perr)
		}
		return append([]Step(nil), pod.Plan...), nil

	case "vm":
		vm, ok := uf.VM[name]
		if !ok {
			return nil, fmt.Errorf("vm %q not found (available: %s)", name, sortedVMKeys(uf))
		}
		return append([]Step(nil), vm.Plan...), nil
	}
	return nil, fmt.Errorf("unhandled include kind %q", kind)
}

func sortedCandyNames(m map[string]*Candy) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func sortedBoxKeys(uf *UnifiedFile) string {
	keys := make([]string, 0, len(uf.Box))
	for k := range uf.Box {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func sortedPodKeys(uf *UnifiedFile) string {
	keys := make([]string, 0, len(uf.Pod))
	for k := range uf.Pod {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func sortedVMKeys(uf *UnifiedFile) string {
	keys := make([]string, 0, len(uf.VM))
	for k := range uf.VM {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
