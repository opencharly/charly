package marketplace

import (
	"fmt"
	"sort"

	"github.com/opencharly/spec/spec"
)

// model.go — the family aggregation. The marketplace entity's `families:` map declares every
// plugin family (the plugins/ directory name); each skill's `family:` field routes it into one.
// A skill referencing an undeclared family is a HARD error (fail-closed — the marketplace
// entity is the single family registry, so a typo'd family cannot silently drop a skill).

// family is one marketplace plugin family: its declared metadata + the skills it owns.
type family struct {
	Name   string
	Meta   spec.MarketplaceFamily
	Skills []spec.Skill // sorted by name; agents (type: "agent") are emitted as agents/*.md
}

// buildModel aggregates the skill/hook entities into the family list, in deterministic order.
func buildModel(ks *kindSet) ([]family, error) {
	// Route every skill into its declared family FIRST (via pointers), then materialize the
	// family list — appending the struct value before routing would snapshot empty skill lists.
	byName := make(map[string]*family, len(ks.Marketplace.Families))
	for name, meta := range ks.Marketplace.Families {
		byName[name] = &family{Name: name, Meta: meta}
	}
	for _, s := range ks.Skills {
		f, ok := byName[s.Family]
		if !ok {
			return nil, fmt.Errorf("skill %q declares family %q which the marketplace entity does not declare (add it to the marketplace `families:` map)", s.Name, s.Family)
		}
		f.Skills = append(f.Skills, s)
	}
	families := make([]family, 0, len(byName))
	for _, f := range byName {
		families = append(families, *f)
	}
	// Deterministic ordering: families by name, skills by name within a family.
	sort.Slice(families, func(i, j int) bool { return families[i].Name < families[j].Name })
	for i := range families {
		sort.Slice(families[i].Skills, func(a, b int) bool { return families[i].Skills[a].Name < families[i].Skills[b].Name })
	}
	return families, nil
}

// familyNames returns the sorted family names (for profiles.json developer membership).
func familyNames(families []family) []string {
	out := make([]string, 0, len(families))
	for _, f := range families {
		out = append(out, f.Name)
	}
	return out
}
