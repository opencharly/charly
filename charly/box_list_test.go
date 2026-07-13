package main

import (
	"errors"
	"testing"
)

// TestResolvedProject_NoProjectIsEmptyNotError proves the resolved-project envelope (which now backs
// `charly box list boxes` / inspect via candy/plugin-box) treats a project-less directory as an EMPTY
// project (nil boxes/candies, no error) rather than a hard "no charly.yml found" failure. This is the
// charly-mcp box.list.boxes case: the MCP server runs the tool in CHARLY_PROJECT_DIR (/workspace)
// before any charly.yml exists, so `box list boxes` must exit 0 with no output. FAILS if
// buildResolvedProjectFromDir propagated LoadConfig's ErrNoCharlyYml instead of returning empty.
func TestResolvedProject_NoProjectIsEmptyNotError(t *testing.T) {
	empty := t.TempDir()

	// LoadConfig on a project-less dir must wrap the ErrNoCharlyYml sentinel so the projector can
	// distinguish "absent project" from a real load failure.
	if _, err := LoadConfig(empty); !errors.Is(err, ErrNoCharlyYml) {
		t.Fatalf("LoadConfig(project-less dir) must wrap ErrNoCharlyYml; got %v", err)
	}

	rp, err := buildResolvedProjectFromDir(empty, ResolveOpts{})
	if err != nil {
		t.Fatalf("buildResolvedProjectFromDir on a project-less dir must not error (empty envelope), got: %v", err)
	}
	if rp == nil {
		t.Fatal("buildResolvedProjectFromDir returned nil envelope")
	}
	if len(rp.Boxes) != 0 || len(rp.Candies) != 0 {
		t.Fatalf("project-less envelope must be empty; got %d boxes, %d candies", len(rp.Boxes), len(rp.Candies))
	}
}
