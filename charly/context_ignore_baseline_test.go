package main

import "testing"

// TestContextIgnoreBaselineFromEmbedded proves the build-context ignore baseline is read
// from the context_ignore_baseline directive in the embedded charly.yml (Phase 4: the data
// moved out of the Go var into the embedded vocab) and matches the canonical set
// byte-for-byte. Fails if the directive is dropped, reordered, or mis-parsed — the
// guard that keeps the emitted .containerignore / .dockerignore unchanged across the move.
func TestContextIgnoreBaselineFromEmbedded(t *testing.T) {
	want := []string{
		".git", "bin", "charly", "*.md",
		"**/__pycache__", "**/*.pyc", "**/*.pyo", "**/*.egg-info",
		"**/node_modules", "**/.git", "**/.DS_Store",
		"**/*~", "**/*.swp", "**/*.swo", "**/.pytest_cache", "**/.mypy_cache",
	}
	if len(baselineContextIgnore) != len(want) {
		t.Fatalf("baseline len=%d, want %d: %v", len(baselineContextIgnore), len(want), baselineContextIgnore)
	}
	for i, w := range want {
		if baselineContextIgnore[i] != w {
			t.Fatalf("baseline[%d]=%q, want %q (embedded charly.yml context_ignore_baseline drift)", i, baselineContextIgnore[i], w)
		}
	}
}
