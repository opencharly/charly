package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// generateVision publishes VISION.md with its H1 dropped (Starlight renders the frontmatter
// title as the page heading) and its repo-relative links rewritten for a web reader. It is NOT
// verbatim, and said so here for a long time while the H1 drop sat twenty lines below.
//
// The vision is the one piece of narrative that already exists in canonical form, and it is
// load-bearing for the site: the tenets are the reason the project looks the way it does. So it
// is projected rather than rewritten — a second, "web-friendly" retelling of the thesis is
// exactly the kind of copy that drifts away from the original and quietly starts lying.
//
// The only edits are mechanical: the repo-relative links in the source (README.md, CLAUDE.md,
// plugins/README.md, CHANGELOG/) point at files a website visitor cannot open, so they are
// rewritten to the corresponding site pages or to GitHub.
func generateVision(root, out string) error {
	raw, err := os.ReadFile(filepath.Join(root, "VISION.md"))
	if err != nil {
		return fmt.Errorf("read VISION.md: %w", err)
	}
	body := string(raw)

	// Drop the source's H1: Starlight renders the frontmatter title as the page heading, so
	// keeping it would show the title twice.
	if strings.HasPrefix(body, "# ") {
		if i := strings.Index(body, "\n"); i >= 0 {
			body = strings.TrimLeft(body[i+1:], "\n")
		}
	}

	for from, to := range visionLinkRewrites {
		body = strings.ReplaceAll(body, from, to)
	}

	return page{
		Path:        "vision.md",
		Title:       "The Vision",
		Description: "The thesis behind the candybox — why OpenCharly secures the room instead of shrinking the toolset.",
		Body:        body,
	}.write(out)
}

// visionLinkRewrites maps the repo-relative targets in VISION.md onto destinations that resolve
// for a website reader. Keyed on the exact markdown link targets used in the source.
var visionLinkRewrites = map[string]string{
	"](README.md)":           "](https://github.com/opencharly/charly#readme)",
	"](CLAUDE.md)":           "](https://github.com/opencharly/charly/blob/main/CLAUDE.md)",
	"](plugins/README.md)":   "](/recipes/)",
	"](CHANGELOG/README.md)": "](https://github.com/opencharly/charly/tree/main/CHANGELOG)",
	"[README.md](README.md)": "[README.md](https://github.com/opencharly/charly#readme)",
	"](VISION.md)":           "](/vision/)",
}
