package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateGrievancesPublishesSource covers the projection: GRIEVANCES.md is published
// verbatim apart from two mechanical edits — the source H1 is dropped (Starlight renders the
// frontmatter title, so keeping it shows the title twice) and the repo-relative links, which a
// website visitor cannot open, are rewritten onto the corresponding site pages.
func TestGenerateGrievancesPublishesSource(t *testing.T) {
	root := writeTree(t, map[string]string{
		"GRIEVANCES.md": strings.Join([]string{
			"# What it is reacting to",
			"",
			"Four properties of ordinary practice.",
			"",
			"Where it is going instead: [the vision](VISION.md).",
			"The mechanisms are catalogued in [the skills](plugins/README.md).",
			"Upstream, [podman](https://podman.io/) does it differently.",
			"",
		}, "\n"),
	})
	out := t.TempDir()

	if err := generateGrievances(root, out); err != nil {
		t.Fatalf("generateGrievances: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(out, "grievances.md"))
	if err != nil {
		t.Fatalf("read emitted grievances.md: %v", err)
	}
	got := string(raw)

	if !strings.Contains(got, "title: \"What it is reacting to\"") {
		t.Errorf("emitted page is missing its frontmatter title:\n%s", got)
	}
	// Without the header the page is invisible to pruneGeneratedPages and would survive as an
	// orphan the moment the generator stopped emitting it.
	if !strings.Contains(got, generatedHeader) {
		t.Errorf("emitted page is missing the generated header:\n%s", got)
	}
	if strings.Contains(got, "# What it is reacting to") {
		t.Errorf("the source H1 should be dropped, the frontmatter title renders it:\n%s", got)
	}
	if !strings.Contains(got, "[the vision](/vision/)") {
		t.Errorf("the VISION.md link was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "[the skills](/recipes/)") {
		t.Errorf("the plugins/README.md link was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "[podman](https://podman.io/)") {
		t.Errorf("an external link was mangled:\n%s", got)
	}
	if !strings.Contains(got, "Four properties of ordinary practice.") {
		t.Errorf("the source body was not published:\n%s", got)
	}
}

// TestGenerateGrievancesMissingSource keeps the generator from emitting an empty page when its
// single source is absent.
func TestGenerateGrievancesMissingSource(t *testing.T) {
	err := generateGrievances(t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected an error when GRIEVANCES.md is absent, got nil")
	}
	if !strings.Contains(err.Error(), "GRIEVANCES.md") {
		t.Errorf("error should name the missing source, got: %v", err)
	}
}
