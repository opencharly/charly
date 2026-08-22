package spec

import (
	"slices"
	"testing"
)

// The pair is MUTUALLY EXCLUSIVE in the CLI, so a caller that strips one and leaves the other
// makes the child exit on "--repo and --dir are mutually exclusive". That failure mode is the
// reason this is one shared function rather than two constants each caller filters by hand,
// so it is what the test pins.
func TestChildProjectEnvStripsBothScopeVars(t *testing.T) {
	in := []string{"PATH=/usr/bin", ProjectDirEnv + "=/parent", ProjectRepoEnv + "=owner/repo", "HOME=/root"}
	got := ChildProjectEnv(in, "")
	for _, kv := range got {
		if kv == ProjectDirEnv+"=/parent" || kv == ProjectRepoEnv+"=owner/repo" {
			t.Fatalf("inherited project scope survived: %q in %v", kv, got)
		}
	}
	for _, want := range []string{"PATH=/usr/bin", "HOME=/root"} {
		if !slices.Contains(got, want) {
			t.Errorf("unrelated variable dropped: %q missing from %v", want, got)
		}
	}
}

func TestChildProjectEnvSetsDirWithoutReintroducingRepo(t *testing.T) {
	in := []string{ProjectDirEnv + "=/parent", ProjectRepoEnv + "=owner/repo"}
	got := ChildProjectEnv(in, "/candy/project")
	if !slices.Contains(got, ProjectDirEnv+"=/candy/project") {
		t.Errorf("projectDir not set: %v", got)
	}
	for _, kv := range got {
		if len(kv) > len(ProjectRepoEnv) && kv[:len(ProjectRepoEnv)] == ProjectRepoEnv {
			t.Fatalf("ProjectRepoEnv survived beside an explicit dir — the child will exit on "+
				"mutual exclusion: %q", kv)
		}
	}
}

func TestChildProjectEnvEmptyDirLeavesNoScopeAtAll(t *testing.T) {
	got := ChildProjectEnv([]string{ProjectDirEnv + "=/parent"}, "")
	if len(got) != 0 {
		t.Errorf("expected no scope variables, got %v", got)
	}
}

// A variable whose NAME merely BEGINS with a scope variable's name is a different variable and
// must survive. This pins exact-name matching (strings.Cut + ==) against a regression to a bare
// strings.HasPrefix(kv, ProjectDirEnv), which would silently eat it.
func TestChildProjectEnvKeepsLookalikeNames(t *testing.T) {
	in := []string{
		ProjectDirEnv + "=/parent",
		ProjectDirEnv + "_LOOKALIKE=keep-me",
		ProjectRepoEnv + "_SUFFIX=keep-me-too",
	}
	got := ChildProjectEnv(in, "/candy/project")
	for _, want := range []string{ProjectDirEnv + "_LOOKALIKE=keep-me", ProjectRepoEnv + "_SUFFIX=keep-me-too"} {
		if !slices.Contains(got, want) {
			t.Errorf("lookalike variable dropped: %q missing from %v", want, got)
		}
	}
	if slices.Contains(got, ProjectDirEnv+"=/parent") {
		t.Errorf("inherited project dir survived: %v", got)
	}
}
