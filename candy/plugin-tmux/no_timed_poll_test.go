package tmux

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestTerminalProviderHasNoTimedPolling locks the event-driven provider rule:
// terminal readiness and lifecycle may block on tmux/gRPC/context events, never
// sleep, tick, or retry against an invented wall-clock duration.
func TestTerminalProviderHasNoTimedPolling(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "terminal.go", nil, 0)
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
			t.Errorf("terminal.go calls time.%s; terminal lifecycle must be event-driven", selector.Sel.Name)
		}
		return true
	})
}
