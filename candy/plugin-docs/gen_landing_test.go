package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRewriteLandingLinks locks in the README→site link projection. The README is written for a
// GitHub reader, so it carries absolute opencharly.ai URLs and repo-relative file paths; neither
// resolves correctly once the same prose IS the site's home page.
func TestRewriteLandingLinks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			// The dominant case: an absolute deep link must become site-relative, or every
			// internal navigation leaves and re-enters over the network.
			name: "absolute site deep link becomes site-relative",
			in:   "See the [recipe cards](https://opencharly.ai/recipes/).",
			want: "See the [recipe cards](/recipes/).",
		},
		{
			name: "nested deep link keeps its whole path",
			in:   "[install](https://opencharly.ai/start/install/)",
			want: "[install](/start/install/)",
		},
		{
			// The bare homepage link is rewritten LAST and separately; doing it first would
			// corrupt every deeper URL into `](/)/recipes/`.
			name: "bare site root becomes /",
			in:   "the [site](https://opencharly.ai) explains it",
			want: "the [site](/) explains it",
		},
		{
			name: "deep link and site root in one body both rewrite",
			in:   "[a](https://opencharly.ai/vision/) and [b](https://opencharly.ai)",
			want: "[a](/vision/) and [b](/)",
		},
		{
			name: "repo-relative AGENTS.md points at GitHub",
			in:   "the [rulebook](AGENTS.md)",
			want: "the [rulebook](https://github.com/opencharly/charly/blob/main/AGENTS.md)",
		},
		{
			name: "plugins README maps onto the recipes index",
			in:   "the [skill index](plugins/README.md)",
			want: "the [skill index](/recipes/)",
		},
		{
			name: "changelog points at the GitHub tree",
			in:   "the [history](CHANGELOG/README.md)",
			want: "the [history](https://github.com/opencharly/charly/tree/main/CHANGELOG)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteLandingLinks(tc.in); got != tc.want {
				t.Errorf("rewriteLandingLinks:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// TestRewriteLandingLinksLeavesForeignLinksAlone is the negative half: the rewriter is keyed on
// opencharly.ai and on the exact repo-relative targets the README uses. Anything else is somebody
// else's URL and must survive untouched — a mangled external link is a worse defect than an
// unrewritten internal one, because nothing on the site can detect it.
func TestRewriteLandingLinksLeavesForeignLinksAlone(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{
			name: "external project link",
			in:   "built on [podman](https://podman.io/docs/).",
		},
		{
			name: "github repo link",
			in:   "[the repo](https://github.com/opencharly/charly)",
		},
		{
			// A host that merely CONTAINS the site name is a different host.
			name: "lookalike host",
			in:   "[not us](https://docs.opencharly.ai.example.com/recipes/)",
		},
		{
			name: "already site-relative link",
			in:   "[start](/start/install/)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteLandingLinks(tc.in); got != tc.in {
				t.Errorf("input was rewritten but should have been left alone:\n got: %q\nwant: %q", got, tc.in)
			}
		})
	}
}

// TestGenerateLandingProjectsReadme covers the whole projection: the README's H1 and tagline are
// dropped (Starlight renders the frontmatter title, and the hero already prints the tagline), the
// site frontmatter is prepended, the generated-file header is emitted so the page is prunable, and
// the body's links are rewritten.
func TestGenerateLandingProjectsReadme(t *testing.T) {
	root := writeTree(t, map[string]string{
		"README.md": strings.Join([]string{
			"# OpenCharly",
			"",
			"**The fully stocked gourmet kitchen for you and your agents.**",
			"",
			"Compose boxes from candies. See the [recipe cards](https://opencharly.ai/recipes/)",
			"and the [rulebook](AGENTS.md); built on [podman](https://podman.io/).",
			"",
		}, "\n"),
	})
	out := t.TempDir()

	if err := generateLanding(root, out); err != nil {
		t.Fatalf("generateLanding: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(out, "index.md"))
	if err != nil {
		t.Fatalf("read emitted index.md: %v", err)
	}
	got := string(raw)

	if !strings.HasPrefix(got, landingFrontmatter) {
		t.Errorf("emitted page does not open with the site frontmatter:\n%s", got)
	}
	// The header is the whole safety boundary for prune.go — without it the landing page becomes
	// an unprunable orphan the moment the generator stops emitting it.
	if !strings.Contains(got, generatedHeader) {
		t.Errorf("emitted page is missing the generated header:\n%s", got)
	}
	if strings.Contains(got, "# OpenCharly\n") {
		t.Errorf("the README H1 should be dropped, Starlight renders the frontmatter title:\n%s", got)
	}
	if strings.Contains(got, "**The fully stocked gourmet kitchen for you and your agents.**") {
		t.Errorf("the tagline should be dropped, the hero already renders it:\n%s", got)
	}
	if !strings.Contains(got, "[recipe cards](/recipes/)") {
		t.Errorf("the absolute site link was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "[rulebook](https://github.com/opencharly/charly/blob/main/AGENTS.md)") {
		t.Errorf("the repo-relative link was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "[podman](https://podman.io/)") {
		t.Errorf("an external link was mangled:\n%s", got)
	}
	if !strings.Contains(got, "Compose boxes from candies.") {
		t.Errorf("the README body was not projected:\n%s", got)
	}
}

// TestGenerateLandingMissingReadme keeps the generator from silently emitting an empty home page
// when its single source is absent.
func TestGenerateLandingMissingReadme(t *testing.T) {
	err := generateLanding(t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected an error when README.md is absent, got nil")
	}
	if !strings.Contains(err.Error(), "README.md") {
		t.Errorf("error should name the missing source, got: %v", err)
	}
}
