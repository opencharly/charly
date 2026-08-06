package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// generate renders every generated page for the opencharly.ai site.
//
// The generator owns the home page (`index.md`, projected from README.md), `vision.md`,
// `grievances.md`, `liberation.md`, `reference/` and `recipes/`, and rewrites them wholesale each
// run. What remains hand-authored is the teaching narrative the repository has no equivalent of:
// the getting-started pages, the concepts curriculum, and the guides.
//
// Everything the site shows that already exists in this repo is GENERATED rather than transcribed.
// A copy drifts; a projection cannot. The home page moved into this list precisely because it was
// the counter-example — two thirds of it was README prose maintained twice, across a submodule
// boundary, under a footnote claiming nothing on the site was a hand-maintained copy.
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
	if err := generateLiberation(root, out); err != nil {
		return err
	}
	// The home page is a projection of README.md — see gen_landing.go for why it stopped being
	// hand-authored. It must be emitted BEFORE the link gates, which check it like any other page.
	if err := generateLanding(root, out); err != nil {
		return err
	}

	// The whole-site link gate runs LAST, over generated and hand-authored pages alike — the
	// harness cross-reference gate above only ever covered `/charly-<plugin>:<skill>` references
	// inside skill bodies, which is why a dead `/recipes/` link once shipped on a green build.
	if err := verifySiteLinks(out); err != nil {
		return err
	}
	// The sidebar lives outside the content tree, so the walk above cannot reach it. See
	// links_sidebar.go for the measured hole this closes.
	if err := verifySidebarLinks(out); err != nil {
		return err
	}

	// Report the prune count alongside the emit counts, and say plainly what it counts: every
	// page carrying the generated header, cleared before this run rewrote the ones it still
	// emits. Almost all of them come straight back, so they are not "stale" — on a healthy run
	// the number is simply the previous run's output.
	//
	// It is deliberately NOT presented as a cleared-vs-written comparison. The counts printed
	// here cover five page families; five further generated pages (reference/providers.md plus
	// the grievances, landing, liberation and vision pages) are not among them, so cleared always
	// exceeds the printed sum by five even when nothing is wrong. A generator that stops emitting
	// something is caught by `task docs:drift` — prune-first means the page simply does not come
	// back, and git reports it as a deletion.
	fmt.Printf("charly docs: %d recipe pages, %d plugin pages (%d provider words), %d cli pages, %d candy pages, %d box pages (%d generated pages cleared before regeneration)\n",
		skillPages, pluginPages, providerWords, cliPages, candyPages, boxPages, pruned)
	return nil
}
