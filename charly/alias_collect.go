package main

import (
	"fmt"
	"regexp"

	"github.com/opencharly/sdk/spec"
)

// alias_collect.go holds the alias CORE residue that OUTLIVES the `charly alias …` CLI extraction
// (the command itself moved to candy/plugin-alias, command:alias). These are NOT the command — each
// is a distinct kernel mechanism the command extraction does not touch, owned by its own cutover:
//
//   - CollectBoxAlias / CollectedAlias — the BUILD-TIME collector: render_baked_metadata.go (the
//     Generator/build-engine helper that bakes OCI labels) bakes the resolved alias set into the
//     image's ai.opencharly.alias OCI label, and labels.go carries it on BoxMetadata.Alias (the
//     OCI-label contract). The projector (resolved_project_host.go) runs it into the
//     ResolvedBoxView box-aggregate `charly box inspect --format aliases` prints.
//   - aliasNameRe — a LOAD-TIME validation regex. NOT consumed anywhere in charly/core (P14-rest
//     trace, 2026-07 found no live call site outside its own test) — candy/plugin-box's own
//     validate_rules.go carries an independent copy that IS wired into `charly box validate`
//     (the box-authoring validation externalization already moved the enforcement there without
//     sweeping this now-orphaned core copy). Flagged for a follow-up dead-code batch rather than
//     removed here (out of this cutover's doc-only scope; removing it would also drop
//     alias_collect_test.go's TestAliasNameRegex pattern-regression coverage unless mirrored into
//     the plugin first).
//
// The `charly box list aliases` enumeration moved into candy/plugin-box (it reads the CandyView.Aliases
// off the resolved-project envelope). The `charly alias` command reads the BAKED label (via `charly box
// labels … --format alias`) and owns the wrapper-script logic; it shares none of the code below.
//
// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K3/K1 — CollectBoxAlias is a
// build-time collector consumed from render_baked_metadata.go (build-cone, K3) and
// resolved_project_host.go (loader-cone, K1). Moves with whichever of those waves lands the
// consumer, not in isolation (P14-rest trace, 2026-07).

// aliasNameRe matches valid alias names: starts with alphanumeric, allows dots/underscores/hyphens
var aliasNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// CollectedAlias represents a resolved alias ready for installation. CUE-sourced in spec
// (boxmetadata.cue, P2B) + aliased in-place; carried on BoxMetadata.Alias (ai.opencharly.alias).
type CollectedAlias = spec.CollectedAlias

// CollectBoxAlias gathers aliases from the box's own candies + box-level config.
// No base chain traversal — aliases are leaf-box specific.
// Candy aliases come first; box-level overrides by name.
func CollectBoxAlias(cfg *Config, layers map[string]*Candy, boxName string) ([]CollectedAlias, error) {
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
