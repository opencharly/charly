package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

func TestCollectImageAliases(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"myapp": {Candy: []string{"svc"}},
		}),
	}
	layers := map[string]spec.CandyReader{
		"svc": testCandy("svc",
			spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}},
			spec.CandyView{Aliases: []spec.CandyAlias{{Name: "svc-cli", Command: "svc-cli-bin"}}},
		),
	}

	aliases, err := CollectBoxAlias(cfg, layers, "myapp")
	if err != nil {
		t.Fatalf("CollectBoxAlias() error = %v", err)
	}

	want := []CollectedAlias{{Name: "svc-cli", Command: "svc-cli-bin"}}
	if !reflect.DeepEqual(aliases, want) {
		t.Errorf("CollectBoxAlias() = %v, want %v", aliases, want)
	}
}

func TestCollectImageAliasesImageOverridesCandy(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"myapp": {
				Candy: []string{"svc"},
				Alias: []vmshared.AliasConfig{{Name: "svc-cli", Command: "custom-cmd"}},
			},
		}),
	}
	layers := map[string]spec.CandyReader{
		"svc": testCandy("svc",
			spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}},
			spec.CandyView{Aliases: []spec.CandyAlias{{Name: "svc-cli", Command: "svc-cli-bin"}}},
		),
	}

	aliases, err := CollectBoxAlias(cfg, layers, "myapp")
	if err != nil {
		t.Fatalf("CollectBoxAlias() error = %v", err)
	}

	if len(aliases) != 1 {
		t.Fatalf("expected 1 alias, got %d", len(aliases))
	}
	if aliases[0].Command != "custom-cmd" {
		t.Errorf("expected image override command, got %q", aliases[0].Command)
	}
}

func TestCollectImageAliasesDefaultCommand(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"myapp": {
				Candy: []string{"svc"},
				Alias: []vmshared.AliasConfig{{Name: "mycli"}}, // no command
			},
		}),
	}
	layers := map[string]spec.CandyReader{
		"svc": testCandy("svc",
			spec.CandyModel{Plan: []spec.Step{{Run: "build", Op: cmdOp("true")}}},
			spec.CandyView{},
		),
	}

	aliases, err := CollectBoxAlias(cfg, layers, "myapp")
	if err != nil {
		t.Fatalf("CollectBoxAlias() error = %v", err)
	}

	if len(aliases) != 1 {
		t.Fatalf("expected 1 alias, got %d", len(aliases))
	}
	if aliases[0].Command != "mycli" {
		t.Errorf("expected command to default to name, got %q", aliases[0].Command)
	}
}

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
