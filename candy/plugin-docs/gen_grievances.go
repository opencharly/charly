package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// generateGrievances publishes GRIEVANCES.md with its H1 dropped and its repo-relative links
// rewritten — the same treatment generateVision applies, and for the same reason it
// publishes VISION.md: it is canonical narrative that already exists, and a second "web-friendly"
// retelling would drift away from the original and quietly start lying.
//
// It is a sibling of the vision rather than part of the README on purpose. The vision states where
// the project is going and this states what it is running away from — both are background, and
// neither belongs in the sixty seconds a first-time reader spends learning what charly is.
//
// The only edits are mechanical: repo-relative links point at files a website visitor cannot open,
// so they are rewritten to the corresponding site pages.
func generateGrievances(root, out string) error {
	raw, err := os.ReadFile(filepath.Join(root, "GRIEVANCES.md"))
	if err != nil {
		return fmt.Errorf("read GRIEVANCES.md: %w", err)
	}
	body := string(raw)

	// Drop the source's H1: Starlight renders the frontmatter title as the page heading, so
	// keeping it would show the title twice.
	if strings.HasPrefix(body, "# ") {
		if i := strings.Index(body, "\n"); i >= 0 {
			body = strings.TrimLeft(body[i+1:], "\n")
		}
	}

	for from, to := range grievanceLinkRewrites {
		body = strings.ReplaceAll(body, from, to)
	}

	return page{
		Path:        "grievances.md",
		Title:       "What it is reacting to",
		Description: "Properties of ordinary container and VM practice that OpenCharly is built to answer — with the mechanism, and where each claim stops being true.",
		Body:        body,
	}.write(out)
}

// grievanceLinkRewrites maps the repo-relative targets in GRIEVANCES.md onto destinations that
// resolve for a website reader. Keyed on the exact markdown link targets used in the source.
var grievanceLinkRewrites = map[string]string{
	"](VISION.md)":         "](/vision/)",
	"](plugins/README.md)": "](/recipes/)",
}
