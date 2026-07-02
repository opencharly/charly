package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScaffoldCandy(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "scaffold-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	// Scaffold a candy
	err = ScaffoldCandy(tmpDir, "test-layer")
	if err != nil {
		t.Fatalf("ScaffoldCandy() error = %v", err)
	}

	// Check directory was created
	candyDir := filepath.Join(tmpDir, "candy", "test-layer")
	if _, err := os.Stat(candyDir); os.IsNotExist(err) {
		t.Error("candy directory was not created")
	}

	// Check the candy manifest was created (the single charly.yml filename)
	candyYml := filepath.Join(candyDir, UnifiedFileName)
	if _, err := os.Stat(candyYml); os.IsNotExist(err) {
		t.Error("candy manifest was not created")
	}
}

func TestScaffoldCandyAlreadyExists(t *testing.T) {
	// Create temp directory with existing candy
	tmpDir, err := os.MkdirTemp("", "scaffold-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	// Create candy directory
	candyDir := filepath.Join(tmpDir, "candy", "existing")
	if err := os.MkdirAll(candyDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Try to scaffold - should fail
	err = ScaffoldCandy(tmpDir, "existing")
	if err == nil {
		t.Error("expected error for existing candy")
	}
}
