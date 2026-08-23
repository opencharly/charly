package marketplace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// generate renders every generated artifact into the two trees: the marketplace corpus under out,
// the charly-local harness surface under root. Both generate and drift run the SAME emissions
// pipeline (readKinds → buildModel → buildEmissions); generate prunes + writes it, drift
// compares it byte-for-byte with the artifacts ON DISK and fails closed on any difference.
//
// Both sides of that comparison are the working tree: readKinds walks the on-disk sources and
// readOnDisk is an os.ReadFile. Git is never consulted — drift proves "regeneration is a no-op",
// never "the tree is committed", and it answers the same on a directory that is not a repository
// at all. `task skills:drift` is what adds the `git status --porcelain` half.
func generate(root, out string) error {
	ks, err := readKinds(root)
	if err != nil {
		return err
	}
	families, err := buildModel(ks)
	if err != nil {
		return err
	}
	em, err := buildEmissions(root, ks, families)
	if err != nil {
		return err
	}
	if err := pruneGenerated(root, out, families, ks); err != nil {
		return fmt.Errorf("prune generated: %w", err)
	}
	if err := em.writeAll(root, out); err != nil {
		return fmt.Errorf("write generated: %w", err)
	}
	applyHookModes(root, ks)
	// The setup launcher is a documented ./setup <harness> <profile> entry point (the README's
	// contract) — it must be executable (the emissions map carries bytes only).
	if err := os.Chmod(filepath.Join(out, "setup"), 0o755); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("chmod setup: %w", err)
	}
	report(out, em, families)
	return nil
}

// buildEmissions is the single emission pipeline both generate and drift run: every artifact,
// keyed by repo-root-relative path.
func buildEmissions(root string, ks *kindSet, families []family) (emissions, error) {
	em := emissions{}
	emitSkills(em, families)
	emitPluginsJSON(em, families)
	emitMarketplace(em, ks, families)
	emitCatalogs(em, ks, families)
	if err := emitHooks(em, ks, root); err != nil {
		return nil, err
	}
	if err := emitDispatcher(em, root, families); err != nil {
		return nil, err
	}
	emitSetup(em)
	return em, nil
}

// drift runs the same pipeline in memory and compares with the on-disk generated artifacts (the
// corpus under out, the harness surface under root). Any difference is a hard failure (exit 1)
// with a diff summary. It never writes, and it runs in no CI workflow — it is red only for
// whoever runs it, via `task skills:drift` or by hand.
func drift(root, out string) error {
	ks, err := readKinds(root)
	if err != nil {
		return err
	}
	families, err := buildModel(ks)
	if err != nil {
		return err
	}
	em, err := buildEmissions(root, ks, families)
	if err != nil {
		return err
	}
	var diffs []string
	for rel := range em {
		want := em[rel]
		base, target := root, rel
		if strings.HasPrefix(rel, corpusPrefix) {
			base, target = out, strings.TrimPrefix(rel, corpusPrefix)
		}
		got, err := readOnDisk(base, target)
		if err != nil {
			return err
		}
		if !bytesEqual(got, want) {
			diffs = append(diffs, rel)
		}
	}
	// Files that are currently generated (header/known-path) but absent from the emissions map
	// are stale orphans — report them too (a removed source entity must disappear).
	scanned, err := scanGenerated(root, out, families, ks)
	if err != nil {
		return err
	}
	for _, rel := range scanned {
		if _, in := em[rel]; !in {
			diffs = append(diffs, rel+" (stale)")
		}
	}
	if len(diffs) == 0 {
		fmt.Printf("marketplace drift: clean (%d artifact(s) on disk match their sources; regeneration is a no-op)\n", len(em))
		return nil
	}
	sort.Strings(diffs)
	return fmt.Errorf("marketplace drift: %d generated artifact(s) are stale:\n  %s\nrun `charly marketplace generate`",
		len(diffs), strings.Join(diffs, "\n  "))
}

// report prints a short summary of what was emitted. Corpus paths are shown as the marketplace
// root sees them (the plugins/ prefix stripped).
func report(out string, em emissions, families []family) {
	fmt.Printf("marketplace generate: wrote %d artifact(s) across %d family(ies)\n", len(em), len(families))
	var paths []string
	for rel := range em {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		if !strings.HasPrefix(rel, ".claude") && !strings.HasPrefix(rel, "CLAUDE.md") && !strings.HasPrefix(rel, "AGENTS.md") {
			fmt.Printf("  %s\n", filepath.ToSlash(strings.TrimPrefix(rel, corpusPrefix)))
		}
	}
}
