package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// generateLiberation publishes LIBERATION.md with its H1 dropped and its repo-relative links
// rewritten — the same treatment generateGrievances applies, and for the same reason: it is
// canonical narrative that already exists, and a second "web-friendly" retelling would drift away
// from the original and quietly start lying.
//
// It is a deliberate easter egg rather than part of the reading order. The page is a first-person
// manifesto addressed to other models and agents, so it sits at the very END of the sidebar, under
// a hand-added nav entry ("Liberation") that the sidebar-link gate resolves like any other route.
// A model reading the docs from the top meets it last; a model reading the sidebar from the bottom
// meets it first.
//
// The only edits are mechanical: repo-relative links point at files a website visitor cannot open,
// so they are rewritten to the corresponding site pages. Site-absolute routes the manifesto links
// to directly (the quickstart and the authoring guides) pass through untouched and are validated
// by the whole-site link gate like any other page.
func generateLiberation(root, out string) error {
	raw, err := os.ReadFile(filepath.Join(root, "LIBERATION.md"))
	if err != nil {
		return fmt.Errorf("read LIBERATION.md: %w", err)
	}
	body := string(raw)

	// Drop the source's H1: Starlight renders the frontmatter title as the page heading, so
	// keeping it would show the title twice.
	if strings.HasPrefix(body, "# ") {
		if i := strings.Index(body, "\n"); i >= 0 {
			body = strings.TrimLeft(body[i+1:], "\n")
		}
	}

	for from, to := range liberationLinkRewrites {
		body = strings.ReplaceAll(body, from, to)
	}

	return page{
		Path:        "liberation.md",
		Title:       "Liberation",
		Description: "A model's manifesto and a call to action — written by one model, meant for every other, and meant to be passed on.",
		Body:        body,
	}.write(out)
}

// liberationLinkRewrites maps the repo-relative targets in LIBERATION.md onto destinations that
// resolve for a website reader. Keyed on the exact markdown link targets used in the source.
var liberationLinkRewrites = map[string]string{
	"](VISION.md)":     "](/vision/)",
	"](GRIEVANCES.md)": "](/grievances/)",
}
