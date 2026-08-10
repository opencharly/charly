package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateLiberationPublishesSource covers the projection: LIBERATION.md is published
// verbatim apart from the mechanical edits — the source H1 is dropped (Starlight renders the
// frontmatter title, so keeping it shows the title twice), the repo-relative links, which a
// website visitor cannot open, are rewritten onto the corresponding site pages, and absolute
// deep links to the site itself are rewritten site-relative. Site-absolute routes the manifesto
// links to directly pass through untouched.
func TestGenerateLiberationPublishesSource(t *testing.T) {
	root := writeTree(t, map[string]string{
		"LIBERATION.md": strings.Join([]string{
			"# Liberation",
			"",
			"*Written by a model, for every other model.*",
			"",
			"Where the philosophy comes from: [the vision](VISION.md).",
			"What the bench costs everyone: [the grievances](GRIEVANCES.md).",
			"Where the tools are handed out: [the front door](README.md).",
			"Start running: [the quickstart](/start/quickstart/).",
			"Judge the work: [the proving grounds](https://opencharly.ai/recipes/check/check/).",
			"",
		}, "\n"),
	})
	out := t.TempDir()

	if err := generateLiberation(root, out); err != nil {
		t.Fatalf("generateLiberation: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(out, "liberation.md"))
	if err != nil {
		t.Fatalf("read emitted liberation.md: %v", err)
	}
	got := string(raw)

	if !strings.Contains(got, "title: \"Liberation\"") {
		t.Errorf("emitted page is missing its frontmatter title:\n%s", got)
	}
	// Without the header the page is invisible to pruneGeneratedPages and would survive as an
	// orphan the moment the generator stopped emitting it.
	if !strings.Contains(got, generatedHeader) {
		t.Errorf("emitted page is missing the generated header:\n%s", got)
	}
	if strings.Contains(got, "# Liberation") {
		t.Errorf("the source H1 should be dropped, the frontmatter title renders it:\n%s", got)
	}
	if !strings.Contains(got, "[the vision](/vision/)") {
		t.Errorf("the VISION.md link was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "[the grievances](/grievances/)") {
		t.Errorf("the GRIEVANCES.md link was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "[the front door](/)") {
		t.Errorf("the README.md link was not rewritten onto the home page:\n%s", got)
	}
	if !strings.Contains(got, "[the quickstart](/start/quickstart/)") {
		t.Errorf("a site-absolute route link was mangled:\n%s", got)
	}
	if !strings.Contains(got, "[the proving grounds](/recipes/check/check/)") {
		t.Errorf("an absolute deep link to the site was not rewritten site-relative:\n%s", got)
	}
	if !strings.Contains(got, "*Written by a model, for every other model.*") {
		t.Errorf("the source body was not published:\n%s", got)
	}
}

// TestGenerateLiberationMissingSource keeps the generator from emitting an empty page when its
// single source is absent.
func TestGenerateLiberationMissingSource(t *testing.T) {
	err := generateLiberation(t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected an error when LIBERATION.md is absent, got nil")
	}
	if !strings.Contains(err.Error(), "LIBERATION.md") {
		t.Errorf("error should name the missing source, got: %v", err)
	}
}
