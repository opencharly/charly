package main

// Regression tests for the check-bed local-candy resolution: a kind:check bed in
// a box/<distro> submodule must test the LATEST LOCAL candies of its parent
// superproject, NOT the pinned remote ones (otherwise the bed serves no purpose —
// it would validate stale code). The bed runner auto-appends a CHARLY_REPO_OVERRIDE
// pointing the parent repo's @github refs at the local working tree (the candy-ref
// analogue of the auto --dev-local-pkg toolchain build). These tests lock the
// merge precedence + the env->local-dir resolution so the behavior can't regress.

import "testing"

func TestMergeRepoOverrides(t *testing.T) {
	cases := []struct{ name, existing, add, want string }{
		{"both empty", "", "", ""},
		{"only auto", "", "github.com/o/r=/dir", "github.com/o/r=/dir"},
		{"only existing", "a/b=/x", "", "a/b=/x"},
		{"operator entries placed FIRST (win on same-repo conflict)", "github.com/o/r=/opdir", "github.com/o/r=/autodir", "github.com/o/r=/opdir,github.com/o/r=/autodir"},
		{"whitespace trimmed", "  a/b=/x  ", "  c/d=/y  ", "a/b=/x,c/d=/y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeRepoOverrides(tc.existing, tc.add); got != tc.want {
				t.Errorf("mergeRepoOverrides(%q,%q) = %q, want %q", tc.existing, tc.add, got, tc.want)
			}
		})
	}
}

// TestRepoOverrideDir_LocalResolution locks the mechanism that makes a check bed
// test LOCAL candies: a CHARLY_REPO_OVERRIDE entry resolves a repo identity to a
// local working tree; the LHS accepts both the full host/owner/repo and bare
// owner/repo forms; an unrelated repo does not match.
func TestRepoOverrideDir_LocalResolution(t *testing.T) {
	dir := t.TempDir()

	t.Setenv(RepoOverrideEnv, "github.com/opencharly/charly="+dir)
	got, ok, err := repoOverrideDir("github.com/opencharly/charly")
	if err != nil || !ok || got != dir {
		t.Fatalf("full LHS: repoOverrideDir = (%q,%v,%v), want (%q,true,nil)", got, ok, err, dir)
	}

	// bare owner/repo LHS also matches (auto github.com prefix — same rule as --repo)
	t.Setenv(RepoOverrideEnv, "opencharly/charly="+dir)
	if got, ok, _ := repoOverrideDir("github.com/opencharly/charly"); !ok || got != dir {
		t.Errorf("bare LHS: got (%q,%v), want (%q,true)", got, ok, dir)
	}

	// an unrelated repo never matches this override
	if _, ok, _ := repoOverrideDir("github.com/other/repo"); ok {
		t.Errorf("unrelated repo should not match the override")
	}
}

// TestRepoOverrideDir_OperatorFirstWins proves an explicit operator override for a
// repo takes precedence over the auto-appended self-superproject entry for the
// same repo (repoOverrideDir returns the FIRST matching pair).
func TestRepoOverrideDir_OperatorFirstWins(t *testing.T) {
	opDir := t.TempDir()
	autoDir := t.TempDir()
	t.Setenv(RepoOverrideEnv, mergeRepoOverrides("github.com/o/r="+opDir, "github.com/o/r="+autoDir))
	got, ok, err := repoOverrideDir("github.com/o/r")
	if err != nil || !ok || got != opDir {
		t.Fatalf("operator-first: got (%q,%v,%v), want operator dir %q", got, ok, err, opDir)
	}
}

// TestSelfSuperprojectOverridePair_NotASubmodule: a plain (non-submodule) dir
// yields no auto-override — its candies already resolve from the local tree, so
// there is nothing to redirect. (The positive submodule case is integration-
// covered by an actual `charly check run <bed>` from a box/<distro> submodule.)
func TestSelfSuperprojectOverridePair_NotASubmodule(t *testing.T) {
	if pair := selfSuperprojectOverridePair(t.TempDir()); pair != "" {
		t.Errorf("non-submodule dir should yield no override, got %q", pair)
	}
}
