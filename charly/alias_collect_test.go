package main

import (
	"testing"
)

// TestCollectImageAliases / TestCollectImageAliasesImageOverridesCandy /
// TestCollectImageAliasesDefaultCommand relocated to candy/plugin-fleet (#55 decoupling,
// Batch A) — they asserted deploykit.CollectBoxAlias directly, zero charly dep.

func TestCandyAliases(t *testing.T) {
	layers, err := ScanCandy("testdata")
	if err != nil {
		t.Fatalf("ScanCandy() error = %v", err)
	}

	ws := layers["webservice"]
	if ws == nil {
		t.Fatal("webservice candy not found")
	}

	if !ws.HasAliases() {
		t.Error("webservice should have aliases")
	}

	aliases := ws.Alias()
	if len(aliases) != 1 {
		t.Fatalf("Aliases() returned %d aliases, want 1", len(aliases))
	}
	if aliases[0].Name != "websvc" {
		t.Errorf("Aliases()[0].Name = %q, want %q", aliases[0].Name, "websvc")
	}
	if aliases[0].Command != "websvc-server" {
		t.Errorf("Aliases()[0].Command = %q, want %q", aliases[0].Command, "websvc-server")
	}
}
