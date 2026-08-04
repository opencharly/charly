package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// generate renders every generated page for the opencharly.ai site.
//
// The generator owns exactly three trees — `vision.md`, `reference/`, and `recipes/` — and
// rewrites them wholesale each run. The hand-authored narrative (the home page, the getting
// started pages, the concept pages, the plugin-authoring guide) is never read or written here:
// those are the pages a website needs and a repo does not have, and they are maintained by hand.
//
// Everything else the site shows already exists in this repo, so it is GENERATED rather than
// transcribed. A copy drifts; a projection cannot.
func generate(root, out string) error {
	if _, err := os.Stat(filepath.Join(root, unifiedFileName)); err != nil {
		return fmt.Errorf("--root %s does not look like an charly project (no %s): %w", root, unifiedFileName, err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("create --out: %w", err)
	}

	// Clear the previous run's output before emitting this one, so a page the generator no longer
	// produces cannot survive as a stale file that every gate above reads as a pass. See prune.go.
	pruned, err := pruneGeneratedPages(out)
	if err != nil {
		return err
	}

	roots, err := repoRoots(root)
	if err != nil {
		return err
	}
	namespaces := make([]string, 0, len(roots))
	for _, r := range roots {
		if r.Namespace == "" {
			namespaces = append(namespaces, "superproject")
			continue
		}
		namespaces = append(namespaces, r.Namespace)
	}
	fmt.Printf("charly docs: walking %d repo root(s): %s\n", len(roots), strings.Join(namespaces, ", "))

	entities, err := collectEntities(roots)
	if err != nil {
		return err
	}
	compiled, err := compiledPlugins(root)
	if err != nil {
		return err
	}
	plugins, err := collectPlugins(root, entities, compiled)
	if err != nil {
		return err
	}
	pluginNames := make(map[string]bool, len(plugins))
	for _, p := range plugins {
		pluginNames[p.Name] = true
	}

	market, err := readMarketplace(root)
	if err != nil {
		return err
	}
	skills, err := collectSkills(root, market)
	if err != nil {
		return err
	}
	resolver := buildResolver(skills, market)

	// Emit. The skill pass collects unresolvable cross-references rather than failing on the
	// first one, so a contributor sees every broken reference in a single run.
	skillPages, dangling, err := generateSkills(out, skills, market, resolver)
	if err != nil {
		return err
	}
	if err := danglingError(dangling); err != nil {
		return err
	}

	pluginPages, err := generatePlugins(out, plugins)
	if err != nil {
		return err
	}
	providerWords, err := generateProviderIndex(out, plugins)
	if err != nil {
		return err
	}
	cliPages, err := generateCLI(out, plugins)
	if err != nil {
		return err
	}
	candyPages, boxPages, err := generateEntities(out, entities, pluginNames)
	if err != nil {
		return err
	}
	if err := generateRecipesIndex(out, skills, market); err != nil {
		return err
	}
	// Count the landing page in the recipes tally. Leaving it out made the generator under-report
	// by one, and that off-by-one was then copied into a CHANGELOG — a tally nobody can reconcile
	// against the tree is worse than no tally.
	skillPages++
	if err := generateVision(root, out); err != nil {
		return err
	}
	if err := generateGrievances(root, out); err != nil {
		return err
	}

	// The whole-site link gate runs LAST, over generated and hand-authored pages alike — the
	// harness cross-reference gate above only ever covered `/charly-<plugin>:<skill>` references
	// inside skill bodies, which is why a dead `/recipes/` link once shipped on a green build.
	if err := verifySiteLinks(out); err != nil {
		return err
	}

	// Report the prune count alongside the emit counts. A run that clears more pages than it
	// writes back is the signal that a generator stopped emitting something, and it is worth
	// seeing rather than inferring from a directory listing.
	fmt.Printf("charly docs: %d recipe pages, %d plugin pages (%d provider words), %d cli pages, %d candy pages, %d box pages (%d stale pages cleared)\n",
		skillPages, pluginPages, providerWords, cliPages, candyPages, boxPages, pruned)
	return nil
}
