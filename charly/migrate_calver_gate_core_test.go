package main

// migrate_calver_gate_core_test.go — the load-time CalVer schema-gate test
// (it exercises the CORE LoadUnified version gate, package-main).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The load-time gate must reject a legacy integer `version: 4` config with a hint
// pointing at the single `charly migrate` command.
func TestLoadUnified_CalVerGateRejectsLegacy(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("version: 4\n")
	_, _, err := LoadUnified(dir)
	if err == nil {
		t.Fatal("expected schema-gate error for version: 4, got nil")
	}
	if !strings.Contains(err.Error(), "charly migrate") {
		t.Errorf("gate error should point at `charly migrate`, got: %v", err)
	}
	// A file stamped to HEAD loads cleanly.
	write("version: " + LatestSchemaVersion().String() + "\n")
	if _, _, err := LoadUnified(dir); err != nil {
		t.Errorf("HEAD-stamped config should load, got: %v", err)
	}
}
