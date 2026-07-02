package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImageReconcile_NewestReferenced is the regression guard for the cross-repo
// pin-alignment tool: mixed pins of one repo collapse to the newest referenced
// version, comments survive, and a second run is a no-op (idempotent). Default
// mode is offline (newest-referenced), so the test is hermetic.
func TestImageReconcile_NewestReferenced(t *testing.T) {
	dir := t.TempDir()
	yml := "" +
		"# top comment\n" +
		"version: 2026.174.1100\n" +
		"import:\n" +
		"  - '@github.com/opencharly/charly/build.yml:v2026.141.1600'\n" +
		"box:\n" +
		"  selkies-desktop:\n" +
		"    base: cachyos.cachyos\n" +
		"    candy:\n" +
		"      - '@github.com/opencharly/charly/layers/agent-forwarding:v2026.141.1600' # infra\n" +
		"      - '@github.com/opencharly/charly/layers/selkies-desktop:v2026.144.0531'\n" +
		"      - '@github.com/opencharly/charly/layers/dbus:v2026.141.1600'\n" +
		"      - '@github.com/opencharly/other/layers/x:v1.0.0'\n"
	path := filepath.Join(dir, "charly.yml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd) //nolint:errcheck

	cmd := &BoxReconcileCmd{}
	if err := cmd.Run(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)

	// Every charly-repo pin aligns to the newest referenced (v2026.144.0531).
	if strings.Contains(s, "v2026.141.1600") {
		t.Errorf("charly pins not aligned to newest; still has v2026.141.1600:\n%s", s)
	}
	for _, want := range []string{
		"build.yml:v2026.144.0531",
		"agent-forwarding:v2026.144.0531",
		"dbus:v2026.144.0531",
		"selkies-desktop:v2026.144.0531",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing aligned pin %q in:\n%s", want, s)
		}
	}
	// A DIFFERENT repo with a single version is left untouched.
	if !strings.Contains(s, "other/layers/x:v1.0.0") {
		t.Errorf("single-version other-repo pin should be untouched:\n%s", s)
	}
	// Comments preserved (node-API edit).
	if !strings.Contains(s, "# top comment") || !strings.Contains(s, "# infra") {
		t.Errorf("comments not preserved:\n%s", s)
	}

	// Idempotent: a second run rewrites nothing.
	if err := cmd.Run(); err != nil {
		t.Fatalf("reconcile (2nd): %v", err)
	}
	got2, _ := os.ReadFile(path)
	if string(got2) != s {
		t.Errorf("reconcile not idempotent:\nfirst:\n%s\nsecond:\n%s", s, string(got2))
	}
}

// TestImageReconcile_VendoredCandyRequires proves reconciliation is FULLY
// automatic: a locally-vendored candy under candy/<n>/candy.yml that pins an
// @github sibling dep in its require: list is aligned too — not just the
// top-level files. Without the candy/ walk in reconcileCandidateFiles this ref
// is never scanned, so it stays at the old version and the resolver keeps
// warning. (Regression guard for the cachyos keepassxc-keyring case.)
func TestImageReconcile_VendoredCandyRequires(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(
		"version: 2026.174.1100\n"+
			"box:\n  foo:\n    base: cachyos.cachyos\n    candy:\n"+
			"      - '@github.com/opencharly/charly/candy/gnupg:v2026.144.0531'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candyDir := filepath.Join(dir, "candy", "keepassxc-keyring")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candyDir, "candy.yml"), []byte(
		"candy:\n  name: keepassxc-keyring\n  version: 2026.174.1100\n  require:\n"+
			"    - '@github.com/opencharly/charly/candy/gnupg:v2026.141.1600' # vendored sibling\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd) //nolint:errcheck
	if err := (&BoxReconcileCmd{}).Run(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(candyDir, "candy.yml"))
	if strings.Contains(string(got), "v2026.141.1600") {
		t.Errorf("vendored candy require not reconciled (still v2026.141.1600):\n%s", got)
	}
	if !strings.Contains(string(got), "gnupg:v2026.144.0531") {
		t.Errorf("vendored candy require not aligned to newest referenced:\n%s", got)
	}
	if !strings.Contains(string(got), "# vendored sibling") {
		t.Errorf("vendored candy comment not preserved:\n%s", got)
	}
}

// TestImageReconcile_SkipsSubmodules proves reconcile does NOT recurse into git
// submodule directories — after the box inversion main/box/ is the submodule
// mount parent, and each charly-project repo reconciles ITSELF.
func TestImageReconcile_SkipsSubmodules(t *testing.T) {
	dir := t.TempDir()
	// Root references gnupg at the newer pin (the reconcile target).
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(
		"version: 2026.174.1100\n"+
			"box:\n  foo:\n    base: fedora\n    candy:\n"+
			"      - '@github.com/opencharly/charly/candy/gnupg:v2026.144.0531'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A submodule under box/ (gitlink `.git` file) whose box pins the OLDER gnupg.
	sub := filepath.Join(dir, "box", "cachyos")
	subBox := filepath.Join(sub, "box", "selkies")
	if err := os.MkdirAll(subBox, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../.git/modules/box/cachyos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	subYML := "box:\n    name: selkies\n    candy:\n" +
		"        - '@github.com/opencharly/charly/candy/gnupg:v2026.141.1600'\n"
	if err := os.WriteFile(filepath.Join(subBox, "charly.yml"), []byte(subYML), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd) //nolint:errcheck
	if err := (&BoxReconcileCmd{}).Run(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// The submodule's pin must be UNTOUCHED (left for the submodule's own reconcile).
	got, _ := os.ReadFile(filepath.Join(subBox, "charly.yml"))
	if !strings.Contains(string(got), "gnupg:v2026.141.1600") {
		t.Errorf("reconcile recursed into a submodule (rewrote its pin):\n%s", got)
	}
}

// TestImageReconcile_NoPins: a project with no @github pins is a clean no-op.
func TestImageReconcile_NoPins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"),
		[]byte("version: 2026.174.1100\nimage:\n  foo:\n    base: fedora\n    candy: [agent-forwarding]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd) //nolint:errcheck
	if err := (&BoxReconcileCmd{}).Run(); err != nil {
		t.Fatalf("reconcile no-pins: %v", err)
	}
}
