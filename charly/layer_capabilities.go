package main

import (
	"fmt"
	"sort"
	"strings"
)

// AggregateCandyCapabilities walks `order` (candy names in topological
// resolution order) and merges each candy's `capabilities:` contribution.
// Returns an error if two candies declare conflicting values for the same
// OCI label key — the conflict surfaces the bug rather than silently
// picking a winner.
func AggregateCandyCapabilities(layers map[string]*Candy, order []string) (*AggregatedCandyCaps, error) {
	out := &AggregatedCandyCaps{
		OCILabels: make(map[string]string),
		Provided:  make(map[string]bool),
	}
	type ociSource struct {
		layer string
		value string
	}
	seen := make(map[string]ociSource)

	for _, name := range order {
		layer, ok := layers[name]
		if !ok || layer == nil {
			continue
		}
		c := layer.capabilities
		if c == nil {
			continue
		}
		if c.PreserveUser {
			out.PreserveUser = true
			out.Provided["preserve_user"] = true
		}
		if c.NeedsRootAfterInit {
			out.NeedsRootAfterInit = true
			out.Provided["needs_root_after_init"] = true
		}
		if c.DataOnly {
			out.DataOnly = true
			out.Provided["data_only"] = true
		}
		if c.InitSystemHint != "" {
			out.InitSystemHint = c.InitSystemHint
			out.Provided["init_system:"+c.InitSystemHint] = true
		}
		for k, v := range c.OCILabels {
			if existing, ok := seen[k]; ok && existing.value != v {
				return nil, fmt.Errorf(
					"capability oci_labels conflict for key %q: layer %q contributes %q, prior layer %q contributes %q",
					k, name, v, existing.layer, existing.value,
				)
			}
			seen[k] = ociSource{layer: name, value: v}
			out.OCILabels[k] = v
		}
	}
	return out, nil
}

// CheckRequiredCapabilities returns a sorted list of capability names
// requested via `requires_capabilities:` on any candy in `order` but not
// provided by the aggregated capabilities. Empty slice on success.
func CheckRequiredCapabilities(layers map[string]*Candy, order []string, agg *AggregatedCandyCaps) []string {
	if agg == nil {
		agg = &AggregatedCandyCaps{Provided: map[string]bool{}}
	}
	missing := make(map[string]bool)
	for _, name := range order {
		layer, ok := layers[name]
		if !ok || layer == nil {
			continue
		}
		for _, req := range layer.requiresCapabilities {
			if !agg.Provided[req] {
				missing[req] = true
			}
		}
	}
	out := make([]string, 0, len(missing))
	for k := range missing {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CandyCapabilitiesError formats a missing-capabilities error with a
// remediation hint pointing at which candies requested what.
func CandyCapabilitiesError(layers map[string]*Candy, order []string, missing []string) error {
	if len(missing) == 0 {
		return nil
	}
	var requesters []string
	for _, name := range order {
		layer, ok := layers[name]
		if !ok || layer == nil {
			continue
		}
		for _, req := range layer.requiresCapabilities {
			for _, m := range missing {
				if req == m {
					requesters = append(requesters, fmt.Sprintf("    %s (requires %s)", name, req))
				}
			}
		}
	}
	return fmt.Errorf(
		"image composition is missing required capabilities: %s\n  layers requesting them:\n%s\n  fix: include a layer that contributes the missing capability via `capabilities:` block",
		strings.Join(missing, ", "),
		strings.Join(requesters, "\n"),
	)
}
