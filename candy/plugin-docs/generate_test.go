package docs

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The leaf generators and gates each have their own unit tests, and every one of them passed while
// generate() was free to not call them at all: deleting the generateLanding, generateGrievances or
// verifySidebarLinks call site from generate() left `go test ./...` fully green. Leaf coverage
// proves a function works; it says nothing about whether the pipeline runs it. These two tests
// cover the wiring, by driving the real entry point and asserting on what the run leaves behind.

// generateSite runs generate() over the real repository into a throwaway site whose sidebar the
// test controls.
//
// The layout mirrors the real one because both gates at the end of generate() depend on it:
// verifySidebarLinks walks UP from the content root for the Astro config, so the config must sit
// above --out rather than inside it, and verifySiteLinks resolves every internal link against the
// pages present in --out — including the hand-authored ones the generator does not own but does
// link to (the README projection alone points at eight of them). Seeding those is therefore part
// of building a site, not a convenience.
func generateSite(t *testing.T, sidebarEntries ...string) (out string, err error) {
	t.Helper()

	root := superprojectRoot(t)
	base := t.TempDir()
	out = filepath.Join(base, "src", "content", "docs")
	if mkErr := os.MkdirAll(out, 0o755); mkErr != nil {
		t.Fatalf("create content root: %v", mkErr)
	}
	if wErr := os.WriteFile(filepath.Join(base, "astro.config.mjs"),
		[]byte(astroConfig(sidebarEntries...)), 0o644); wErr != nil {
		t.Fatalf("write astro config: %v", wErr)
	}
	seedHandAuthoredPages(t, root, out)

	return out, generate(root, out)
}

// superprojectRoot resolves the repository root this package lives two levels below. generate()
// reads the real corpus — candies, boxes, plugins, skills — so there is no synthetic stand-in for
// it. Named for the tree rather than for `repoRoot`, which sources.go already uses for the type
// describing one walked project.
func superprojectRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, unifiedFileName)); err != nil {
		t.Fatalf("resolved repo root %s does not hold %s: %v", root, unifiedFileName, err)
	}
	return root
}

// seedHandAuthoredPages copies the committed site's hand-authored pages into a fresh --out.
//
// They are selected by the same rule pruneGeneratedPages uses — a page is generated if and only if
// it carries the generated header — rather than by a hardcoded list of directories, so a new
// hand-authored tree needs no change here and, more importantly, so nothing generated can leak in.
// That exclusion is what makes the assertions in these tests airtight: a seeded page can never
// carry the generated header, so a page that carries one was written by the run under test.
func seedHandAuthoredPages(t *testing.T, root, out string) {
	t.Helper()

	src := filepath.Join(root, "docs", "src", "content", "docs")
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("no committed content tree at %s — the docs submodule is not checked out "+
			"(git submodule update --init docs): %v", src, err)
	}

	header := []byte(generatedHeader)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".mdx":
		default:
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, header) {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(out, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, raw, 0o644)
	})
	if err != nil {
		t.Fatalf("seed hand-authored pages from %s: %v", src, err)
	}
}

// TestGenerateWiresLeafGenerators proves generate() actually calls the leaf generators, by
// asserting on the pages a real run leaves behind. Each page is checked for the generated header
// as well as for existence: the seed deliberately excludes every header-carrying page, so the
// header is proof the run wrote the file rather than the fixture.
func TestGenerateWiresLeafGenerators(t *testing.T) {
	out, genErr := generateSite(t,
		"      { label: 'The Vision', link: '/vision/' },",
		"      { label: 'Recipes', link: '/recipes/' },",
	)

	// The page assertions run BEFORE the error is reported, and deliberately so. Removing a leaf
	// call site can also trip the site-link gate — the README projection links to /grievances/, so
	// dropping generateGrievances surfaces as a dead link — and that error alone never says which
	// call site went missing. Naming the unwired caller is the whole point of this test.
	for _, tc := range []struct {
		page   string
		caller string
	}{
		{page: "index.md", caller: "generateLanding"},
		{page: "grievances.md", caller: "generateGrievances"},
		{page: "liberation.md", caller: "generateLiberation"},
	} {
		raw, err := os.ReadFile(filepath.Join(out, tc.page))
		if err != nil {
			t.Errorf("generate() did not emit %s — its %s call site is not wired: %v",
				tc.page, tc.caller, err)
			continue
		}
		if !bytes.Contains(raw, []byte(generatedHeader)) {
			t.Errorf("%s carries no generated header, so it is the seeded fixture rather than "+
				"output of %s", tc.page, tc.caller)
		}
	}

	if genErr != nil {
		t.Fatalf("generate: %v", genErr)
	}
}

// TestGeneratePreservesHeaderlessFilesUnderGeneratedTrees is the test that failed the validator's
// resetTree-vs-prune finding. resetTree removed whole trees BY PATH before regenerating — an
// unread os.RemoveAll that would have deleted a hand-authored page living under a generated
// directory. prune-first makes it redundant: every emitted page carries the generated header, so
// pruning by header already clears the orphans, and a file without the header is preserved
// precisely because the boundary is content, not location. This test plants a headerless file
// under reference/candy/ and asserts generate() leaves it alone.
func TestGeneratePreservesHeaderlessFilesUnderGeneratedTrees(t *testing.T) {
	root := superprojectRoot(t)
	base := t.TempDir()
	out := filepath.Join(base, "src", "content", "docs")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("create content root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "astro.config.mjs"),
		[]byte(astroConfig("      { label: 'The Vision', link: '/vision/' },")), 0o644); err != nil {
		t.Fatalf("write astro config: %v", err)
	}
	seedHandAuthoredPages(t, root, out)

	kept := filepath.Join(out, "reference", "candy", "keep-me.md")
	if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
		t.Fatalf("mkdir reference/candy: %v", err)
	}
	if err := os.WriteFile(kept, []byte("Hand-authored under a generated tree.\n"), 0o644); err != nil {
		t.Fatalf("write keep-me.md: %v", err)
	}

	if err := generate(root, out); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("generate() removed a headerless file under reference/candy by path: %v", err)
	}
}

// TestGenerateWiresSidebarGate proves generate() runs the sidebar gate. The gate is the only one
// that reads the Astro config, so a sidebar entry pointing at a page the run never emits fails
// through verifySidebarLinks and nothing else — which is what makes this a wiring test for that
// call site specifically.
func TestGenerateWiresSidebarGate(t *testing.T) {
	_, err := generateSite(t,
		"      { label: 'The Vision', link: '/vision/' },",
		"      { label: 'Ghost', link: '/no-such-page/' },",
	)
	if err == nil {
		t.Fatal("generate() succeeded with a dead sidebar target — " +
			"its verifySidebarLinks call site is not wired")
	}
	if !strings.Contains(err.Error(), "/no-such-page/") {
		t.Errorf("error should name the dead sidebar target, got: %v", err)
	}
}
