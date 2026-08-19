package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
)

// The prescan is what puts an out-of-process command word into the Kong grammar, and the grammar
// is frozen by kong.Parse — which runs BEFORE main()'s --repo chdir. So the prescan has to resolve
// --repo itself; if it only ever looked at cwd, `charly --repo <owner/repo> <word>` reported an
// unknown verb from any directory that was not already a project. Reading charly.yml worked (that
// happens after the chdir); finding the verb did not.
//
// These tests pin the argv/env scanning half. The resolution half (ResolveProjectRepo) is covered
// by main_repo_test.go, and the end-to-end behaviour by the check-commands-local bed.

func TestScanRepoFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"absent", []string{"charly", "box", "list", "boxes"}, ""},
		{"separate form", []string{"charly", "--repo", "opencharly/charly", "mcp", "serve"}, "opencharly/charly"},
		{"equals form", []string{"charly", "--repo=opencharly/charly", "mcp", "serve"}, "opencharly/charly"},
		{"pinned ref", []string{"charly", "--repo", "opencharly/charly@main", "mcp"}, "opencharly/charly@main"},
		{"trailing flag with no value", []string{"charly", "--repo"}, ""},
		// argv[0] is skipped, so a binary that happens to be named --repo is not read as a flag —
		// and with no other --repo present the result is empty, not the next token.
		{"argv0 ignored", []string{"--repo", "box", "list"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := scanRepoFlag(tc.args); got != tc.want {
				t.Fatalf("scanRepoFlag(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// projectDirPreParse's precedence is load-bearing: CHARLY_PROJECT_DIR wins over -C/--dir, which
// wins over --repo, which wins over cwd. A regression here silently sends the prescan at the wrong
// project, and the symptom is an unknown verb rather than an error.
func TestProjectDirPreParse_Precedence(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	t.Setenv(spec.ProjectDirEnv, dirA)
	t.Setenv(spec.ProjectRepoEnv, "")
	if got := projectDirPreParse(); got != dirA {
		t.Fatalf("CHARLY_PROJECT_DIR must win: got %q, want %q", got, dirA)
	}

	// With the env var cleared, an explicit -C wins over cwd.
	t.Setenv(spec.ProjectDirEnv, "")
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })
	os.Args = []string{"charly", "-C", dirB, "box", "validate"}
	if got := projectDirPreParse(); got != dirB {
		t.Fatalf("-C must win over cwd: got %q, want %q", got, dirB)
	}

	// No dir signal at all falls back to cwd — never to the empty string, which would silently
	// disable the prescan and take every project-provided verb out of the grammar.
	os.Args = []string{"charly", "box", "validate"}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if got := projectDirPreParse(); got != wd {
		t.Fatalf("bare invocation must fall back to cwd: got %q, want %q", got, wd)
	}
}

// A --repo spec that cannot resolve must FALL BACK TO CWD, not take the local grammar down with
// it. main() resolves the same spec moments later and reports "cannot resolve --repo" properly;
// the prescan returning "" is strictly worse, because the user sees "unknown verb" instead.
//
// This test previously asserted nothing — it called projectDirPreParse() and discarded the value,
// on the reasoning that a warm cache might make an unresolvable spec resolve. That reasoning was
// wrong (the spec below cannot resolve on any host, warm cache or not) and it let a real defect
// ship: the implementation returned "" here, so from inside a real project
// `charly --repo <bad> mcp …` died on `unexpected argument mcp, did you mean "cp"?` — the exact
// unknown-verb dead end this cutover removes. An unasserted test is not coverage.
func TestProjectDirPreParse_UnresolvableRepoFallsBackToCwd(t *testing.T) {
	t.Setenv(spec.ProjectDirEnv, "")
	t.Setenv(spec.ProjectRepoEnv, "")
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	// An owner/repo that cannot exist: the name embeds a fresh temp-dir basename, so no cache
	// on any host can contain it and resolution must genuinely fail.
	unresolvable := "definitely/not-a-real-repo-" + filepath.Base(t.TempDir())

	os.Args = []string{"charly", "--repo", unresolvable, "mcp"}
	if got := projectDirPreParse(); got != wd {
		t.Errorf("an unresolvable --repo must fall back to cwd so the LOCAL grammar survives; "+
			"got %q, want %q (a %q return makes a project-provided verb report 'unknown verb' "+
			"instead of 'cannot resolve --repo')", got, wd, "")
	}

	// Same contract via the env var, which takes the sibling branch.
	os.Args = []string{"charly", "mcp"}
	t.Setenv(spec.ProjectRepoEnv, unresolvable)
	if got := projectDirPreParse(); got != wd {
		t.Errorf("an unresolvable CHARLY_PROJECT_REPO must fall back to cwd; got %q, want %q", got, wd)
	}
}
