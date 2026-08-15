package docs

import (
	"fmt"
	"sort"
	"strings"
)

// bucketOrder is the reading order of the four marketplace categories: what a newcomer wants
// first (run the verbs), then authoring, then the deployable catalog, then contributor internals.
var bucketOrder = []string{"commands", "kind", "images", "development"}

// generateRecipesIndex emits the landing page for the recipe-card section.
//
// It exists because `/recipes/` is a REAL DESTINATION, not just a sidebar group. The site links
// there from the home page, from the quickstart, and from the generated vision page (whose
// rewrite table maps the repo's `plugins/README.md` onto it) — and a sidebar group label emits no
// route, so without this page all three were 404s on a published site.
func generateRecipesIndex(outRoot string, skills []skill, m *marketplace) error {
	byDir := map[string][]skill{}
	for _, s := range skills {
		byDir[s.PluginDir] = append(byDir[s.PluginDir], s)
	}
	byCategory := map[string][]marketplacePlugin{}
	for _, p := range m.Plugins {
		byCategory[p.Category] = append(byCategory[p.Category], p)
	}

	var b strings.Builder
	b.WriteString("Every candy, box, verb and subsystem in OpenCharly ships a **recipe card**: what a " +
		"factory floor runs on, and what an agent loads as a skill. Each card is a dedicated page " +
		"describing what the thing does, how it is made, and how it should behave — the same cards an " +
		"agent loads while working on the project, published here unchanged.\n\n")
	fmt.Fprintf(&b, "%d cards across %d plugins, in four groups.\n", len(skills), len(m.Plugins))

	seen := map[string]bool{}
	for _, cat := range bucketOrder {
		plugins := byCategory[cat]
		if len(plugins) == 0 {
			continue
		}
		seen[cat] = true
		writeBucket(&b, cat, plugins, byDir)
	}
	// Any category the manifest grows later still gets a section rather than vanishing.
	var extra []string
	for cat := range byCategory {
		if !seen[cat] {
			extra = append(extra, cat)
		}
	}
	sort.Strings(extra)
	for _, cat := range extra {
		writeBucket(&b, cat, byCategory[cat], byDir)
	}

	return page{
		Path:        "recipes/index.md",
		Title:       "Recipe cards",
		Description: "A dedicated page for every candy, box, verb and subsystem — the same cards agents load as skills.",
		Body:        b.String(),
	}.write(outRoot)
}

func writeBucket(b *strings.Builder, cat string, plugins []marketplacePlugin, byDir map[string][]skill) {
	fmt.Fprintf(b, "\n## %s\n\n", categoryLabel(cat))
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	for _, p := range plugins {
		cards := byDir[p.Dir()]
		if len(cards) == 0 {
			continue
		}
		fmt.Fprintf(b, "### %s\n\n", p.Name)
		if d := strings.TrimSpace(firstLine(p.Description)); d != "" {
			fmt.Fprintf(b, "%s\n\n", d)
		}
		sort.Slice(cards, func(i, j int) bool { return cards[i].Name < cards[j].Name })
		for _, c := range cards {
			fmt.Fprintf(b, "- [%s](%s)\n", c.Title, skillSitePath(c))
		}
		b.WriteString("\n")
	}
}
