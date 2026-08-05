package docs

import (
	"fmt"
	"strings"
)

func candyPagePath(seg string) string     { return "reference/candy/" + seg + ".md" }
func candySitePathFor(seg string) string  { return "/reference/candy/" + seg + "/" }
func boxPagePath(seg string) string       { return "reference/box/" + seg + ".md" }
func pluginSitePathFor(seg string) string { return "/reference/plugin/" + seg + "/" }

// generateEntities emits one page per DEFINED candy and box — including boxes that carry
// `enabled: false` (an entity not built by default is still an entity someone can compose) and
// including every candy vendored inside a box/<distro> submodule.
func generateEntities(outRoot string, entities []entity, pluginNames map[string]bool) (candies, boxes int, err error) {
	for _, e := range entities {
		if e.IsBox {
			if err := writeBoxPage(outRoot, e); err != nil {
				return 0, 0, err
			}
			boxes++
			continue
		}
		if err := writeCandyPage(outRoot, e, pluginNames[e.Name]); err != nil {
			return 0, 0, err
		}
		candies++
	}
	return candies, boxes, nil
}

func writeCandyPage(outRoot string, e entity, isPlugin bool) error {
	c := e.Candy
	var b strings.Builder

	b.WriteString("| | |\n|---|---|\n")
	if v := e.Version(); v != "" {
		fmt.Fprintf(&b, "| **Version** | `%s` |\n", v)
	}
	if e.Namespace != "" {
		fmt.Fprintf(&b, "| **Repo** | `box/%s` |\n", e.Namespace)
	} else {
		b.WriteString("| **Repo** | superproject |\n")
	}
	if c.Status != "" {
		fmt.Fprintf(&b, "| **Status** | %s |\n", c.Status)
	}
	if isPlugin {
		fmt.Fprintf(&b, "| **Plugin** | yes — see the [plugin reference](%s) |\n",
			pluginSitePathFor(e.PathSegment()))
	}
	b.WriteString("\n")

	if d := strings.TrimSpace(e.Description()); d != "" {
		b.WriteString(d)
		b.WriteString("\n")
	}

	if names := c.Package.Names(); len(names) > 0 {
		b.WriteString("\n## Packages\n\nInstalled on every distro:\n\n")
		for _, n := range names {
			fmt.Fprintf(&b, "- `%s`\n", n)
		}
	}

	if len(c.Service) > 0 {
		b.WriteString("\n## Services\n\n")
		for _, s := range c.Service {
			if s.Name != "" {
				fmt.Fprintf(&b, "- `%s`\n", s.Name)
			}
		}
	}

	writeAcceptancePlan(&b, c.Plan)

	return page{
		Path:        candyPagePath(e.PathSegment()),
		Title:       e.Name,
		Description: e.Description(),
		Body:        b.String(),
	}.write(outRoot)
}

func writeBoxPage(outRoot string, e entity) error {
	bx := e.Box
	var b strings.Builder

	b.WriteString("| | |\n|---|---|\n")
	if v := e.Version(); v != "" {
		fmt.Fprintf(&b, "| **Version** | `%s` |\n", v)
	}
	if e.Namespace != "" {
		fmt.Fprintf(&b, "| **Repo** | `box/%s` |\n", e.Namespace)
	}
	if bx.Base != "" {
		fmt.Fprintf(&b, "| **Base** | `%s` |\n", bx.Base)
	}
	if bx.From != "" {
		fmt.Fprintf(&b, "| **Builder** | `%s` |\n", bx.From)
	}
	// An `enabled: false` box is still a defined box; say so plainly rather than omitting it.
	if bx.Enabled != nil && !*bx.Enabled {
		b.WriteString("| **Default build** | not built by default (`enabled: false`) |\n")
	}
	b.WriteString("\n")

	if d := strings.TrimSpace(e.Description()); d != "" {
		b.WriteString(d)
		b.WriteString("\n")
	}

	if len(bx.Candy) > 0 {
		b.WriteString("\n## Composition\n\nThis box composes:\n\n")
		for _, ref := range bx.Candy {
			fmt.Fprintf(&b, "- `%v`\n", ref)
		}
	}

	return page{
		Path:        boxPagePath(e.PathSegment()),
		Title:       e.Name,
		Description: e.Description(),
		Body:        b.String(),
	}.write(outRoot)
}

// writeAcceptancePlan renders a candy's `plan:` as its published acceptance spec. Every candy
// ships a non-empty description and at least one deterministic check — the spec IS the test —
// and publishing it lets a reader see what "working" means without cloning anything.
func writeAcceptancePlan(b *strings.Builder, plan []stepView) {
	if len(plan) == 0 {
		return
	}
	type entry struct{ intent, prose string }
	var entries []entry
	for _, s := range plan {
		intent, prose := s.Intent()
		if intent == "" {
			continue
		}
		entries = append(entries, entry{intent, prose})
	}
	if len(entries) == 0 {
		return
	}
	b.WriteString("\n## Acceptance plan\n\n")
	b.WriteString("This candy's `plan:` — the runnable spec `charly check` executes against a live " +
		"deployment. `check:` steps are idempotent probes; `run:` steps change state.\n\n")
	b.WriteString("| Intent | Step |\n|---|---|\n")
	for _, e := range entries {
		fmt.Fprintf(b, "| `%s` | %s |\n", e.intent, mdCell(e.prose))
	}
}

// mdCell flattens authored prose into a single table cell.
func mdCell(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}
