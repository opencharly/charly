package docs

import (
	"path/filepath"
	"strings"
	"testing"
)

// astroConfig renders a minimal Astro/Starlight config carrying the given sidebar entries. The
// gate reads the `link:` targets as static string literals, so the surrounding shape only has to
// be realistic, not executable.
func astroConfig(entries ...string) string {
	return strings.Join([]string{
		"import { defineConfig } from 'astro/config';",
		"import starlight from '@astrojs/starlight';",
		"",
		"export default defineConfig({",
		"  integrations: [starlight({",
		"    title: 'OpenCharly',",
		"    sidebar: [",
		strings.Join(entries, "\n"),
		"    ],",
		"  })],",
		"});",
		"",
	}, "\n")
}

// sidebarTree materializes a site whose content root is nested below the Astro config, which is
// the real layout: the config lives at the docs-site root and the content collection several
// levels below it. It returns the content root to pass as --out.
func sidebarTree(t *testing.T, config string, content map[string]string) string {
	t.Helper()
	files := map[string]string{"astro.config.mjs": config}
	for rel, body := range content {
		files["src/content/docs/"+rel] = body
	}
	return filepath.Join(writeTree(t, files), "src", "content", "docs")
}

// TestVerifySidebarLinksPassesWhenComplete also proves the walk UP from the content root: the
// config sits three directories above --out and must still be found, because a hardcoded `../../..`
// would break silently the first time the content root moved.
func TestVerifySidebarLinksPassesWhenComplete(t *testing.T) {
	out := sidebarTree(t, astroConfig(
		"      { label: 'The Vision', link: '/vision/' },",
		"      { label: 'Install', link: \"/start/install/\" },",
		"      { label: 'Home', link: '/' },",
	), map[string]string{
		"vision.md":        "The vision.\n",
		"start/install.md": "Install.\n",
		"index.mdx":        "Home.\n",
	})

	if err := verifySidebarLinks(out); err != nil {
		t.Errorf("expected every resolving sidebar entry to pass, got: %v", err)
	}
}

// TestVerifySidebarLinksCatchesDeadTarget is the regression guard for the measured hole: pointing
// a sidebar entry at a nonexistent page builds `853 page(s) built`, exit 0 — and then renders that
// dead link on EVERY page of the site. the docs repo's build, its drift gate and
// verifySiteLinks each miss it for their own structural reason.
func TestVerifySidebarLinksCatchesDeadTarget(t *testing.T) {
	out := sidebarTree(t, astroConfig(
		"      { label: 'The Vision', link: '/vision/' },",
		"      { label: 'Ghost', link: '/no-such-page/' },",
	), map[string]string{
		"vision.md": "The vision.\n",
	})

	err := verifySidebarLinks(out)
	if err == nil {
		t.Fatal("expected a dead sidebar target to be reported, got nil")
	}
	if !strings.Contains(err.Error(), "/no-such-page/") {
		t.Errorf("error should name the dead target, got: %v", err)
	}
	if strings.Contains(err.Error(), "/vision/") {
		t.Errorf("a resolving sidebar entry was reported as dead: %v", err)
	}
	if !strings.Contains(err.Error(), "astro.config.mjs") {
		t.Errorf("error should name the config it read, got: %v", err)
	}
}

// TestVerifySidebarLinksIgnoresNonPageTargets keeps the gate from firing on entries that are not
// routes, matching verifySiteLinks: assets served from public/ and on-page anchors.
func TestVerifySidebarLinksIgnoresNonPageTargets(t *testing.T) {
	out := sidebarTree(t, astroConfig(
		"      { label: 'Logo', link: '/favicon.svg' },",
		"      { label: 'Anchor', link: '/vision/#why' },",
		"      { label: 'Query', link: '/vision/?x=1' },",
		"      { label: 'The Vision', link: '/vision/' },",
	), map[string]string{
		"vision.md": "The vision.\n",
	})

	if err := verifySidebarLinks(out); err != nil {
		t.Errorf("expected non-page sidebar targets to be ignored, got: %v", err)
	}
}

// TestVerifySidebarLinksMissingConfigIsAnError locks in the deliberate refusal to skip: a gate
// that silently checks nothing is worse than no gate, so an unfindable config fails the run.
func TestVerifySidebarLinksMissingConfigIsAnError(t *testing.T) {
	out := writeTree(t, map[string]string{"vision.md": "The vision.\n"})

	err := verifySidebarLinks(out)
	if err == nil {
		t.Fatal("expected an error when no astro config can be found, got nil")
	}
	if !strings.Contains(err.Error(), "astro.config.mjs") {
		t.Errorf("error should name the filenames it looked for, got: %v", err)
	}
}
