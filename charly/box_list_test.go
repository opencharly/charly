package main

import (
	"errors"
	"os"
	"testing"
)

// TestListBoxes_NoProjectIsEmptyNotError proves `charly box list boxes` treats a
// project-less directory as an EMPTY list (exit 0, no output) rather than a hard
// "no charly.yml found" error. This is the charly-mcp box.list.boxes case: the MCP
// server runs the tool in CHARLY_PROJECT_DIR (/workspace) before any charly.yml
// exists. FAILS against the pre-fix code (LoadConfig's error propagated, exit 1).
func TestListBoxes_NoProjectIsEmptyNotError(t *testing.T) {
	empty := t.TempDir()

	// LoadConfig on a project-less dir must wrap the ErrNoCharlyYml sentinel so
	// read commands can distinguish "absent project" from a real load failure.
	if _, err := LoadConfig(empty); !errors.Is(err, ErrNoCharlyYml) {
		t.Fatalf("LoadConfig(project-less dir) must wrap ErrNoCharlyYml; got %v", err)
	}

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	if err := os.Chdir(empty); err != nil {
		t.Fatal(err)
	}
	if err := (&ListBoxesCmd{}).Run(); err != nil {
		t.Fatalf("box list boxes in a project-less dir must exit 0 (empty list), got: %v", err)
	}
}
