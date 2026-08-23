package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// sidebarLinkPattern matches a site-absolute `link:` target in the Astro config's sidebar array —
// `{ label: 'The Vision', link: '/vision/' }`. Sidebar entries are static string literals, so a
// pattern match reads them exactly; nothing here evaluates JavaScript.
var sidebarLinkPattern = regexp.MustCompile(`(?m)link:\s*["'](/[^"']*)["']`)

// astroConfigNames are the filenames Astro accepts for its config, newest form first.
var astroConfigNames = []string{"astro.config.mjs", "astro.config.js", "astro.config.ts"}

// verifySidebarLinks resolves the Astro config's sidebar `link:` targets against the routes the
// site emits.
//
// The sidebar is the one navigation surface no other gate can see, and it is the one that fails
// most widely when it breaks. It was measured rather than assumed: pointing an entry at a
// nonexistent page and running the production build gives `853 page(s) built`, exit 0 — and then
// renders that dead link on every page that has a sidebar. Three gates miss it, each for its own
// structural reason:
//
//   - the docs repo's Astro build — Astro does not resolve sidebar link targets.
//   - the docs repo's drift gate — compares a regeneration of the CONTENT tree; it never reads
//     the config.
//   - verifySiteLinks — walks `.md`/`.mdx` under the content root, so a file outside that root is
//     beyond its reach by construction.
//
// So the hole is not an oversight in any one of them; it is the seam between them, which is
// exactly the shape of gap that survives review. It is closed here because this is where the
// project already knows how to resolve a site route.
//
// The config lives outside `--out` (the content root is nested several levels below it), so it is
// found by walking UP from the content root rather than by a hardcoded `../../..`, which would
// break silently the first time the content root moved. A config that cannot be found is reported
// rather than skipped: silently checking nothing is the failure mode this whole function exists to
// remove.
func verifySidebarLinks(outRoot string) error {
	configPath, err := findAstroConfig(outRoot)
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}

	routes, _, err := collectRoutes(outRoot)
	if err != nil {
		return err
	}

	var dead []string
	for _, m := range sidebarLinkPattern.FindAllStringSubmatch(string(raw), -1) {
		target := m[1]
		if i := strings.IndexAny(target, "#?"); i >= 0 {
			target = target[:i]
		}
		// Non-page assets are not routes, matching verifySiteLinks.
		if target == "" || filepath.Ext(target) != "" {
			continue
		}
		if !routes[normalizeRoute(target)] {
			dead = append(dead, m[1])
		}
	}
	if len(dead) == 0 {
		return nil
	}

	dead = dedupe(dead)
	sort.Strings(dead)
	var b strings.Builder
	fmt.Fprintf(&b, "%d sidebar link target(s) in %s resolve to no page:\n",
		len(dead), filepath.Base(configPath))
	for _, t := range dead {
		fmt.Fprintf(&b, "  %s\n", t)
	}
	b.WriteString("a dead sidebar entry renders on every page of the site")
	return fmt.Errorf("%s", b.String())
}

// findAstroConfig walks up from the content root looking for the Astro config. Walking up rather
// than assuming a fixed depth keeps this working if the content root ever moves, and reaching the
// filesystem root without a hit is an error, never a silent pass.
func findAstroConfig(outRoot string) (string, error) {
	dir, err := filepath.Abs(outRoot)
	if err != nil {
		return "", fmt.Errorf("resolve --out: %w", err)
	}
	for {
		for _, name := range astroConfigNames {
			candidate := filepath.Join(dir, name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no astro config (%s) found in or above %s — "+
				"the sidebar link gate cannot run, and a gate that silently checks nothing is worse than none",
				strings.Join(astroConfigNames, ", "), outRoot)
		}
		dir = parent
	}
}
