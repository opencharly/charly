package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadCandyRecord_GatesPreCutover proves the ledger read path hard-rejects a
// pre-cutover record (no schema_version) with an actionable error.
func TestReadCandyRecord_GatesPreCutover(t *testing.T) {
	root := t.TempDir()
	layers := filepath.Join(root, "layers")
	if err := os.MkdirAll(layers, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layers, "old.json"), []byte(`{"layer":"old","deployed_by":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadCandyRecord(&LedgerPaths{Root: root, Candies: layers}, "old")
	if err == nil {
		t.Fatal("expected gate error on a pre-cutover record")
	}
	if !strings.Contains(err.Error(), "pre-cutover install-ledger record") {
		t.Errorf("gate error should explain the pre-cutover record: %v", err)
	}
}
