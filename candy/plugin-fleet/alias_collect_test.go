package fleet

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// alias_collect_test.go — relocated (in part) from charly/alias_collect_test.go (#55
// decoupling, Batch A): these 3 tests assert deploykit.CollectBoxAlias directly, zero charly
// dep. TestCandyAliases (uses charly's own ScanCandy("testdata")) stays in charly.

func TestCollectImageAliases(t *testing.T) {
	cfg := &spec.Config{
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

	aliases, err := deploykit.CollectBoxAlias(cfg, layers, "myapp")
	if err != nil {
		t.Fatalf("CollectBoxAlias() error = %v", err)
	}

	want := []spec.CollectedAlias{{Name: "svc-cli", Command: "svc-cli-bin"}}
	if !reflect.DeepEqual(aliases, want) {
		t.Errorf("CollectBoxAlias() = %v, want %v", aliases, want)
	}
}

func TestCollectImageAliasesImageOverridesCandy(t *testing.T) {
	cfg := &spec.Config{
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

	aliases, err := deploykit.CollectBoxAlias(cfg, layers, "myapp")
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
	cfg := &spec.Config{
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

	aliases, err := deploykit.CollectBoxAlias(cfg, layers, "myapp")
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
