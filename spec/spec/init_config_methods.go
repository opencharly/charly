package spec

import (
	"path/filepath"
	"slices"
)

// init_config_methods.go — the `init:` build-vocabulary detection / resolution METHODS on the
// CUE-SOURCED spec.InitConfig type (generated from #InitConfig in schema/init.cue; the struct itself
// lives in cue_types_gen.go). #55 Cluster-B import-purity: relocated from sdk/buildkit DOWN to spec
// (the value leaf, alongside the CUE-sourced DistroConfig/BuilderConfig vocabulary) so charly core
// reads it over its spec+proto-only import surface; sdk/buildkit keeps a thin type-alias for its
// plugin callers. These are pure Go METHODS CUE cannot express — mirroring spec/distro_config_methods.go
// beside the generated DistroConfig. Pure over spec.CandyReader / spec.ResolvedInit /
// spec.AggregatedCandyCaps — no loader/registry coupling (kit.SortStrings replaced by the stdlib
// slices.Sort, since spec cannot import sdk/kit).

// DetectCandyInit returns which init system names a candy triggers,
// based on its candy manifest fields and file patterns.
func (ic *InitConfig) DetectCandyInit(ly *CandyYAML, candyPath string) []string {
	if ic == nil {
		return nil
	}
	var result []string
	for initName, def := range ic.Init {
		if detectsInit(def, ly, candyPath) {
			result = append(result, initName)
		}
	}
	slices.Sort(result)
	return result
}

// detectsInit checks if a candy matches an init system's detection criteria.
// Schema-driven: iterates the unified service: list + per-entry init routing
// (IsPackaged → ServiceSchema.SupportsPackaged; custom exec → ServiceSchema.ServiceTemplate).
func detectsInit(def *ResolvedInit, ly *CandyYAML, candyPath string) bool {
	if ly == nil {
		return false
	}
	// candy_field: [service] gates schema-driven detection.
	participatesInSchema := slices.Contains(def.CandyFields, "service")
	if participatesInSchema {
		for i := range ly.Service {
			entry := &ly.Service[i]
			if entry.IsPackaged() {
				if def.ServiceSchema != nil && def.ServiceSchema.SupportsPackaged {
					return true
				}
			} else {
				if def.ServiceSchema != nil && def.ServiceSchema.ServiceTemplate != "" {
					return true
				}
			}
		}
	}

	// candy_file: glob the candy dir (file_copy model — systemd *.service units).
	for _, pattern := range def.CandyFiles {
		matches, _ := filepath.Glob(filepath.Join(candyPath, pattern))
		if len(matches) > 0 {
			return true
		}
	}

	return false
}

// ResolveInitSystem determines the active init system for an image.
// Priority: explicit override → auto-detect from candies.
// Returns ("", nil) if no init system is needed.
//
// Candy capability requirements (RequiresCapabilities) are checked
// against the aggregated candy caps for the composition; init systems
// whose requirements aren't met are filtered out. The aggregated caps
// are also consulted for the bootc-prefer-systemd heuristic via
// PreserveUser (the canonical signal that this is a bootc-flavored
// composition).
func (ic *InitConfig) ResolveInitSystem(layers map[string]CandyReader, candyOrder []string, explicit string) (string, *ResolvedInit) {
	if ic == nil {
		return "", nil
	}

	// ONE filtered-candidate computation, shared with ActiveInit. An init is a
	// candidate only if a candy triggers it AND its RequiresCapability set is
	// satisfied, and both rules live in ActiveInit alone — this function applies
	// neither itself.
	//
	// This sharing is load-bearing, not tidiness: EmitInitAssembly enables a
	// use_packaged: system unit through the image's RESOLVED init only
	// (`if initName == img.InitSystem`), so correctness depends on the resolved
	// name always being a key of ActiveInit. While the trigger scan and the
	// capability filter were written out twice, that invariant held only because
	// the two copies happened to agree — and the explicit-override branch below
	// returned before the filter was ever applied, so an override naming an init
	// whose capability was unmet resolved to a non-key and every system unit in
	// the image went silently un-enabled.
	candidates, caps := ic.activeInitWithCaps(layers, candyOrder)

	// Explicit override, honored only when it names a viable candidate. An
	// override for an init no candy triggers, or whose RequiresCapability is
	// unmet, falls through to auto-detect rather than resolving to a name that
	// nothing can enable.
	if explicit != "" {
		if def, ok := candidates[explicit]; ok {
			return explicit, def
		}
	}

	// For bootc-flavored compositions (preserve_user) prefer systemd over supervisord
	if caps.PreserveUser && candidates["systemd"] != nil {
		return "systemd", candidates["systemd"]
	}

	// For container images, prefer supervisord
	if def := candidates["supervisord"]; def != nil {
		return "supervisord", def
	}

	// Return first remaining init system
	for initName, def := range candidates {
		return initName, def
	}

	return "", nil
}

// ActiveInit returns all init systems that are active for the given image.
// An image can have multiple active inits (e.g., supervisord + systemd on
// bootc-flavored compositions).
func (ic *InitConfig) ActiveInit(layers map[string]CandyReader, candyOrder []string) map[string]*ResolvedInit {
	result, _ := ic.activeInitWithCaps(layers, candyOrder)
	return result
}

// activeInitWithCaps computes the active-init set AND returns the aggregated
// capabilities it filtered with, so a caller needing both (ResolveInitSystem,
// for the preserve_user preference) aggregates once rather than twice. The
// aggregation walks every candy in the order, so a second call is pure
// duplicated work in a function whose whole point is computing this once.
func (ic *InitConfig) activeInitWithCaps(layers map[string]CandyReader, candyOrder []string) (map[string]*ResolvedInit, *AggregatedCandyCaps) {
	if ic == nil {
		return nil, nil
	}

	caps, _ := AggregateCandyCapabilities(layers, candyOrder)
	if caps == nil {
		caps = &AggregatedCandyCaps{Provided: map[string]bool{}}
	}

	result := make(map[string]*ResolvedInit)
	for _, candyName := range candyOrder {
		layer, ok := layers[candyName]
		if !ok {
			continue
		}
		for initName, def := range ic.Init {
			if !layer.HasInit(initName) {
				continue
			}
			if !initDefRequirementsMet(def, caps) {
				continue
			}
			result[initName] = def
		}
		// port_relay triggers init systems with relay_template
		if len(layer.RelayPorts()) > 0 {
			for initName, def := range ic.Init {
				if def.RelayTemplate != "" && initDefRequirementsMet(def, caps) {
					result[initName] = def
				}
			}
		}
	}

	return result, caps
}

// initDefRequirementsMet reports whether the init definition's
// RequiresCapabilities are all present in the aggregated caps.
func initDefRequirementsMet(def *ResolvedInit, caps *AggregatedCandyCaps) bool {
	if def == nil || len(def.RequiresCapability) == 0 {
		return true
	}
	if caps == nil || caps.Provided == nil {
		return false
	}
	for _, req := range def.RequiresCapability {
		if !caps.Provided[req] {
			return false
		}
	}
	return true
}

// InitNames returns a sorted list of all init system names.
func (ic *InitConfig) InitNames() []string {
	if ic == nil {
		return nil
	}
	names := make([]string, 0, len(ic.Init))
	for name := range ic.Init {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
