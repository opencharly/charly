package main

import (
	"github.com/opencharly/sdk/spec"
	"reflect"
	"testing"
)

func TestCollectImageAliases(t *testing.T) {
	cfg := &Config{
		Box: boxMapOf(map[string]spec.BoxConfig{
			"myapp": {Candy: []string{"svc"}},
		}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name:    "svc",
			plan:    []spec.Step{{Run: "build", Op: cmdOp("true")}},
			aliases: []AliasYAML{{Name: "svc-cli", Command: "svc-cli-bin"}},
		},
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
				Alias: []AliasConfig{{Name: "svc-cli", Command: "custom-cmd"}},
			},
		}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name:    "svc",
			plan:    []spec.Step{{Run: "build", Op: cmdOp("true")}},
			aliases: []AliasYAML{{Name: "svc-cli", Command: "svc-cli-bin"}},
		},
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
				Alias: []AliasConfig{{Name: "mycli"}}, // no command
			},
		}),
	}
	layers := map[string]*Candy{
		"svc": {
			Name: "svc",
			plan: []spec.Step{{Run: "build", Op: cmdOp("true")}},
		},
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

func TestAliasNameRegex(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"openclaw", true},
		{"my-tool", true},
		{"my_tool", true},
		{"my.tool", true},
		{"MyTool", true},
		{"tool123", true},
		{"1start", true},
		{"", false},
		{"-start", false},
		{".start", false},
		{"_start", false},
		{"has space", false},
		{"has/slash", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aliasNameRe.MatchString(tt.name)
			if got != tt.want {
				t.Errorf("aliasNameRe.MatchString(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
