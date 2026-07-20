package main

import (
	"fmt"

	"github.com/opencharly/sdk/spec"
)

// alias_collect.go holds the alias CORE residue that OUTLIVES the `charly alias …` CLI extraction
// (the command itself moved to candy/plugin-alias, command:alias). This is NOT the command — it
// is a distinct kernel mechanism the command extraction does not touch, owned by its own cutover:
//
//   - CollectBoxAlias / CollectedAlias — the BUILD-TIME collector: render_baked_metadata.go (the
//     Generator/build-engine helper that bakes OCI labels) bakes the resolved alias set into the
//     image's ai.opencharly.alias OCI label, and labels.go carries it on BoxMetadata.Alias (the
//     OCI-label contract). The projector (resolved_project_host.go) runs it into the
//     ResolvedBoxView box-aggregate `charly box inspect --format aliases` prints.
//
// The LOAD-TIME alias-name validation regex (formerly a core copy here, aliasNameRe) was DELETED
// (P14-adjacent dead-code sweep, 2026-07): it had no production call site in charly/core — the
// box-authoring validation externalization had already moved the real enforcement to
// candy/plugin-box/validate_rules.go's own independent copy without sweeping this orphan. Its
// pattern-regression test moved with it (candy/plugin-box/validate_pure_test.go
// TestAliasNameRegex, against the live copy).
//
// The `charly box list aliases` enumeration moved into candy/plugin-box (it reads the CandyView.Aliases
// off the resolved-project envelope). The `charly alias` command reads the BAKED label (via `charly box
// labels … --format alias`) and owns the wrapper-script logic; it shares none of the code below.
//
// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K3/K1 — CollectBoxAlias is a
// build-time collector consumed from render_baked_metadata.go (build-cone, K3) and
// resolved_project_host.go (loader-cone, K1). Moves with whichever of those waves lands the
// consumer, not in isolation (P14-rest trace, 2026-07).

// CollectedAlias represents a resolved alias ready for installation. CUE-sourced in spec
// (boxmetadata.cue, P2B) + aliased in-place; carried on BoxMetadata.Alias (ai.opencharly.alias).
type CollectedAlias = spec.CollectedAlias

// CollectBoxAlias gathers aliases from the box's own candies + box-level config.
// No base chain traversal — aliases are leaf-box specific.
// Candy aliases come first; box-level overrides by name.
func CollectBoxAlias(cfg *Config, layers map[string]spec.CandyReader, boxName string) ([]CollectedAlias, error) {
	img, ok := cfg.BoxConfig(boxName)
	if !ok {
		return nil, fmt.Errorf("box %q not found in charly.yml", boxName)
	}

	// Resolve candies for this box (leaf-specific — aliases do NOT inherit from
	// a base box; the shared boxDirectCandies walk).
	resolved, err := cfg.boxDirectCandies(layers, boxName)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var result []CollectedAlias

	// Collect from candies
	for _, candyName := range resolved {
		layer, ok := layers[candyName]
		if !ok || !layer.HasAliases() {
			continue
		}
		for _, a := range layer.Alias() {
			if seen[a.Name] {
				continue
			}
			seen[a.Name] = true
			result = append(result, CollectedAlias(a))
		}
	}

	// Collect from box config (overrides candy aliases with same name)
	for _, a := range img.Alias {
		cmd := a.Command
		if cmd == "" {
			cmd = a.Name
		}
		if seen[a.Name] {
			// Override: find and replace
			for i := range result {
				if result[i].Name == a.Name {
					result[i].Command = cmd
					break
				}
			}
		} else {
			seen[a.Name] = true
			result = append(result, CollectedAlias{Name: a.Name, Command: cmd})
		}
	}

	return result, nil
}
