package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCharlyDir_FlagChdir verifies that -C / --dir / CHARLY_PROJECT_DIR causes
// main() to chdir before dispatching, so downstream os.Getwd() calls
// (used by every build-mode command to locate charly.yml) see the target
// directory. Covers three modes:
//
//  1. -C <path>  — short flag
//  2. --dir <path> — long flag
//  3. CHARLY_PROJECT_DIR=<path> env var
//
// Uses `charly box list boxes` as the probe: it reads charly.yml from the
// resolved project dir. If the binary fails to chdir, the command errors
// with "no charly.yml found: ... no such file or directory". A pass means
// chdir worked — the command listed the boxes from the scratch project.
func TestCharlyDir_FlagChdir(t *testing.T) {
	bin := buildCharlyBinary(t)

	scratch := t.TempDir()
	writeMinProject(t, scratch)

	// Spawn each variant from /tmp (a guaranteed-different cwd so a missed
	// chdir would fail loudly).
	startCwd := os.TempDir()

	cases := []struct {
		name string
		args []string
		env  []string
	}{
		{name: "short flag -C", args: []string{"-C", scratch, "box", "list", "boxes"}},
		{name: "long flag --dir", args: []string{"--dir", scratch, "box", "list", "boxes"}},
		{name: "env var CHARLY_PROJECT_DIR", args: []string{"box", "list", "boxes"}, env: []string{"CHARLY_PROJECT_DIR=" + scratch}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			cmd.Dir = startCwd
			cmd.Env = append(append([]string{}, os.Environ()...), tc.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("charly %s failed: %v\noutput: %s", strings.Join(tc.args, " "), err, out)
			}
			if !strings.Contains(string(out), "testimage") {
				t.Errorf("charly box list boxes did not see the scratch project's image; output:\n%s", out)
			}
		})
	}
}

// TestCharlyDir_Errors covers the error paths: non-existent directory, and a
// file (not a directory) passed to --dir.
func TestCharlyDir_Errors(t *testing.T) {
	bin := buildCharlyBinary(t)

	scratch := t.TempDir()
	notADir := filepath.Join(scratch, "not-a-dir")
	if err := os.WriteFile(notADir, []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	missing := filepath.Join(scratch, "missing")

	cases := []struct {
		name string
		dir  string
		want string
	}{
		{name: "missing dir", dir: missing, want: "cannot chdir"},
		{name: "file not dir", dir: notADir, want: "cannot chdir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, "-C", tc.dir, "version")
			cmd.Dir = os.TempDir()
			out, _ := cmd.CombinedOutput()
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("expected error containing %q, got: %s", tc.want, out)
			}
		})
	}
}

// buildCharlyBinary compiles charly into the test temp dir. Cached per-test via
// t.TempDir. Go build is <1s on a warm module cache, well within test
// budget.
func buildCharlyBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "charly")
	cmd := exec.Command("go", "build", "-o", out, ".")
	if buildOut, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, buildOut)
	}
	return out
}

// writeMinProject materialises a minimal charly.yml in dir so `charly box list
// boxes` has something to parse. Mirrors the scaffold of a real project.
func writeMinProject(t *testing.T, dir string) {
	t.Helper()
	// Compact node-form: every entity flattens to a top-level
	// `<name>: {<kind>: <FULL BODY>}` node with its collections (here `distro:`)
	// INLINE in the kind value. LoadConfig reads charly.yml exclusively.
	charlyYAML := `version: 2026.201.2300
defaults:
  registry: ghcr.io/test
  tag: latest
  platform:
    - linux/amd64
  build: [rpm]

testimage:
  candy:
    base: "quay.io/fedora/fedora:43"
    distro: ["fedora:43", fedora]
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(charlyYAML), 0644); err != nil {
		t.Fatalf("writing charly.yml: %v", err)
	}
}
