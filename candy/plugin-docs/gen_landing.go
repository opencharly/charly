package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// generateLanding projects README.md onto the site's home page.
//
// The landing page used to be hand-authored, and roughly two thirds of it was the README copied
// across a submodule boundary. Two hand-maintained copies of the same prose in two repositories
// drift, and this pair had already started: "Podman and Docker" in one and "Podman and docker" in
// the other, a table column present in one and dropped in the other, a claim corrected in one and
// left stale in the other. The site's own footnote meanwhile promised that "nothing here is a
// hand-maintained copy" — printed on the page that was the largest one.
//
// So the home page becomes what the reference and recipe halves already are: a projection. There
// is one source for what charly is, it lives next to the code, and the two surfaces cannot
// disagree because only one of them is written.
//
// The frontmatter is NOT taken from the README, because it is site machinery rather than content:
// the hero, the Starlight template choice, and the <title> override. It is assembled here, and the
// reasons for each choice are recorded in the emitted comments so the next person to touch it does
// not have to rediscover them.
func generateLanding(root, out string) error {
	raw, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		return fmt.Errorf("read README.md: %w", err)
	}
	body := string(raw)

	// Drop the H1 — Starlight renders the frontmatter title as the page heading.
	if strings.HasPrefix(body, "# ") {
		if i := strings.Index(body, "\n"); i >= 0 {
			body = strings.TrimLeft(body[i+1:], "\n")
		}
	}
	// Drop the tagline line that follows it: the hero already renders exactly this sentence, and
	// showing it twice within one screen is the first thing a reader notices.
	body = landingTaglinePattern.ReplaceAllString(body, "")

	body = rewriteLandingLinks(body)

	dest := filepath.Join(out, "index.md")
	if err := os.WriteFile(dest, []byte(landingFrontmatter+generatedHeader+"\n\n"+
		strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write index.md: %w", err)
	}
	return nil
}

var landingTaglinePattern = regexp.MustCompile(`(?m)\A\*\*The fully stocked gourmet kitchen[^\n]*\n\n`)

// rewriteLandingLinks maps the README's link targets onto destinations that resolve on the site.
//
// The dominant case is the README's own absolute links to opencharly.ai: on the site itself those
// must become site-relative, or every internal navigation leaves and re-enters over the network,
// and the whole-site link gate cannot see them. The bare https://opencharly.ai homepage link is
// rewritten last and separately — rewriting it first would corrupt every deeper URL.
func rewriteLandingLinks(body string) string {
	// Deep links first: https://opencharly.ai/foo/ -> /foo/
	body = rewriteSiteAbsoluteDeepLinks(body)
	for from, to := range landingLinkRewrites {
		body = strings.ReplaceAll(body, from, to)
	}
	return body
}

// landingLinkRewrites covers the repo-relative targets and the bare site root. Repo-relative
// markdown files are not published, so they point at GitHub; a website reader following them gets
// the real file rather than a 404.
var landingLinkRewrites = map[string]string{
	"](https://opencharly.ai)": "](/)",
	"](AGENTS.md)":             "](https://github.com/opencharly/charly/blob/main/AGENTS.md)",
	"](plugins/README.md)":     "](/recipes/)",
	"](CHANGELOG/README.md)":   "](https://github.com/opencharly/charly/tree/main/CHANGELOG)",
}

// landingFrontmatter is the site machinery the README has no business carrying.
const landingFrontmatter = `---
title: OpenCharly
description: The fully stocked gourmet kitchen for you and your agents — a tiny core, an all-you-can-eat buffet.
# ` + "`doc`" + ` rather than ` + "`splash`" + `, deliberately: Starlight hard-codes the table of contents to false for
# splash pages and ignores the ` + "`tableOfContents`" + ` frontmatter entirely, and ` + "`hasSidebar`" + ` is likewise
# ` + "`template !== 'splash'`" + `. So a splash landing page cannot have either sidebar. The hero renders on
# whichever template carries a ` + "`hero:`" + ` block, so ` + "`doc`" + ` keeps the hero while gaining both.
template: doc
# Starlight appends the site title to every page title, so a home page called "OpenCharly" on a site
# called "OpenCharly" renders the tab as "OpenCharly | OpenCharly". Override the whole <title> here
# only; every other page keeps the useful "Page | OpenCharly" form.
head:
  - tag: title
    content: OpenCharly — the fully stocked gourmet kitchen for you and your agents
hero:
  tagline: The fully stocked gourmet kitchen for you and your agents — a tiny core, an all-you-can-eat buffet.
  actions:
    - text: Get started
      link: /start/install/
      icon: right-arrow
    - text: Quickstart
      link: /start/quickstart/
      variant: minimal
    - text: View on GitHub
      link: https://github.com/opencharly/charly
      icon: external
      variant: minimal
---

`
