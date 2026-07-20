package check

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestBedRunnerHasNoTimedRetry(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "bed_run.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	banned := map[string]bool{"After": true, "Sleep": true, "NewTicker": true, "NewTimer": true, "Tick": true}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !banned[selector.Sel.Name] {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if ok && ident.Name == "time" {
			t.Errorf("bed_run.go calls time.%s; R10 steps must run once and synchronize on explicit readiness", selector.Sel.Name)
		}
		return true
	})
}

func TestCheckStepCommandSummaryDoesNotEchoArbitraryArguments(t *testing.T) {
	secret := "do-not-print-this-secret"
	got := checkStepCommandSummary([]string{"cmd", "target", secret})
	if got != "charly cmd" {
		t.Fatalf("summary = %q, want safe command boundary without target payload", got)
	}
}
