package proc

// Regression tests for the check-bed local-candy resolution (relocated from
// charly/repo_override_test.go, #55 W3 B2-full): a kind:check bed in a box/<distro> submodule
// must test the LATEST LOCAL candies of its parent superproject, NOT the pinned remote ones
// (otherwise the bed serves no purpose — it would validate stale code). The bed runner
// auto-appends a CHARLY_REPO_OVERRIDE pointing the parent repo's @github refs at the local working
// tree (the candy-ref analogue of the auto --dev-local-pkg toolchain build). These tests lock the
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
			if got := MergeRepoOverrides(tc.existing, tc.add); got != tc.want {
				t.Errorf("MergeRepoOverrides(%q,%q) = %q, want %q", tc.existing, tc.add, got, tc.want)
			}
		})
	}
}

// TestRepoOverrideDir_LocalResolution / TestRepoOverrideDir_OperatorFirstWins (repoOverrideDir's
// detailed parsing behavior — LHS resolution, multi-pair precedence) live in
// sdk/loaderkit/refs_collect_test.go (K1 unit 4): repoOverrideDir itself relocated there, taking
// the env VALUE as an explicit parameter rather than reading os.Getenv internally. The
// end-to-end override short-circuit through the PUBLIC EnsureRepoDownloaded wrapper (via
// t.Setenv) stays covered in charly/refs_fresh_test.go's TestEnsureRepoDownloaded_Override.

// TestSelfSuperprojectOverridePair_NotASubmodule: a plain (non-submodule) dir
// yields no auto-override — its candies already resolve from the local tree, so
// there is nothing to redirect. (The positive submodule case is integration-
// covered by an actual `charly check run <bed>` from a box/<distro> submodule.)
func TestSelfSuperprojectOverridePair_NotASubmodule(t *testing.T) {
	if pair := SelfSuperprojectOverridePair(t.TempDir()); pair != "" {
		t.Errorf("non-submodule dir should yield no override, got %q", pair)
	}
}
