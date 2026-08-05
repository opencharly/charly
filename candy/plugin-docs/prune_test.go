package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// generatedPage renders a page the way emit.go does, so the fixtures carry the real marker rather
// than a hand-copied approximation of it.
func generatedPage(body string) string {
	return "---\ntitle: \"x\"\n---\n\n" + generatedHeader + "\n\n" + body + "\n"
}

// TestPruneGeneratedPagesRemovesOnlyGeneratedPages is the regression guard for the measured hole
// prune.go exists to close: with no prune, a page whose generator was removed survives forever and
// BOTH gates above it read as a pass (drift sees an identical file; the link gate resolves links
// into it). The header is the whole safety boundary, so the hand-authored narrative must survive
// untouched.
func TestPruneGeneratedPagesRemovesOnlyGeneratedPages(t *testing.T) {
	root := writeTree(t, map[string]string{
		"reference/candy/ripgrep.md": generatedPage("A candy page."),
		"recipes/check/check.mdx":    generatedPage("A recipe card."),
		"start/install.md":           "---\ntitle: Install\n---\n\nHand-authored.\n",
		"concepts/index.mdx":         "Hand-authored too.\n",
		// Not markdown: the extension filter must skip it even though it carries the marker.
		"assets/pages.json": "{\"note\": \"" + generatedHeader + "\"}\n",
	})

	pruned, err := pruneGeneratedPages(root)
	if err != nil {
		t.Fatalf("pruneGeneratedPages: %v", err)
	}
	if pruned != 2 {
		t.Errorf("pruned count = %d, want 2", pruned)
	}

	for _, rel := range []string{"reference/candy/ripgrep.md", "recipes/check/check.mdx"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("generated page %s survived the prune (stat err: %v)", rel, err)
		}
	}
	for _, rel := range []string{"start/install.md", "concepts/index.mdx", "assets/pages.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("non-generated file %s was deleted: %v", rel, err)
		}
	}
}

// TestPruneGeneratedPagesClearsEmptiedDirs covers the second half: a directory that held only
// generated pages is now empty, and an empty directory in a content collection is noise. A
// directory that still holds a hand-authored page — and the content root itself — must remain.
func TestPruneGeneratedPagesClearsEmptiedDirs(t *testing.T) {
	root := writeTree(t, map[string]string{
		"reference/candy/ripgrep.md": generatedPage("A candy page."),
		"reference/box/fedora.md":    generatedPage("A box page."),
		"guides/deploy.md":           generatedPage("A generated guide."),
		"guides/handwritten.md":      "Hand-authored.\n",
	})

	if _, err := pruneGeneratedPages(root); err != nil {
		t.Fatalf("pruneGeneratedPages: %v", err)
	}

	for _, rel := range []string{"reference/candy", "reference/box", "reference"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("emptied directory %s survived (stat err: %v)", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "guides")); err != nil {
		t.Errorf("a directory still holding a hand-authored page was removed: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("the content root itself was removed: %v", err)
	}
}

// TestPruneGeneratedPagesOnCleanTree keeps the prune honest on the case that must do nothing: a
// tree with no generated pages at all reports zero and changes nothing.
func TestPruneGeneratedPagesOnCleanTree(t *testing.T) {
	root := writeTree(t, map[string]string{
		"index.mdx":        "Hand-authored.\n",
		"start/install.md": "Hand-authored.\n",
	})

	pruned, err := pruneGeneratedPages(root)
	if err != nil {
		t.Fatalf("pruneGeneratedPages: %v", err)
	}
	if pruned != 0 {
		t.Errorf("pruned count = %d on a hand-authored tree, want 0", pruned)
	}
	for _, rel := range []string{"index.mdx", "start/install.md"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("hand-authored file %s was deleted: %v", rel, err)
		}
	}
}

// TestPruneGeneratedPagesReportsUnreadableTree keeps the scan from swallowing an I/O failure: a
// prune that silently reports success is exactly the "looks complete but is not" state the whole
// file exists to prevent.
func TestPruneGeneratedPagesReportsUnreadableTree(t *testing.T) {
	_, err := pruneGeneratedPages(filepath.Join(t.TempDir(), "no-such-out"))
	if err == nil {
		t.Fatal("expected an error scanning a nonexistent --out, got nil")
	}
	if !strings.Contains(err.Error(), "scan --out") {
		t.Errorf("error should name the scan that failed, got: %v", err)
	}
}
