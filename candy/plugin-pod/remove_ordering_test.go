package pod

// remove_ordering_test.go — pins the ONE load-bearing ordering invariant of quadlet teardown:
// the container-removal assertion must run BEFORE any quadlet file is deleted.
//
// Why that order is load-bearing: a quadlet .container file IS the unit's definition. Once it is
// deleted and the daemon reloaded, the unit ceases to exist, and with it any means of ever
// reaping a container that outlived it. A container still alive at that moment is orphaned
// PERMANENTLY — holding its netns and its aardvark-dns registration, which breaks container-name
// resolution host-wide for unrelated deploys. Reaping after the delete would therefore be too
// late by construction, not merely racy.
//
// Why this test is STRUCTURAL rather than behavioural: runPodRemove is not callable in a unit
// test — it resolves the live runtime, shells out to systemctl, reads the real quadlet directory,
// and pulls sidecar names over the plugin reverse channel. Making it injectable enough to drive
// end-to-end would be a substantial refactor of a function whose behaviour is otherwise proven by
// the check-sidecar-pod bed. Asserting the call order over the AST costs nothing, cannot drift
// from the source (it reads the source), and fails loudly if someone later moves the reap below
// the deletes — which is exactly the regression that would silently reintroduce the orphan.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestQuadletTeardownReapsContainersBeforeDeletingUnitFiles(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "remove_orchestration.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing remove_orchestration.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == "runPodRemove" {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatal("runPodRemove not found in remove_orchestration.go")
	}

	var reapPos, firstRemovePos token.Pos
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			if f.Name == "ensureContainersRemoved" && !reapPos.IsValid() {
				reapPos = call.Pos()
			}
		case *ast.SelectorExpr:
			// os.Remove / os.RemoveAll — the unit-file deletions.
			pkg, ok := f.X.(*ast.Ident)
			if ok && pkg.Name == "os" && (f.Sel.Name == "Remove" || f.Sel.Name == "RemoveAll") && !firstRemovePos.IsValid() {
				firstRemovePos = call.Pos()
			}
		}
		return true
	})

	if !reapPos.IsValid() {
		t.Fatal("runPodRemove no longer calls ensureContainersRemoved — a container surviving `systemctl stop` would be orphaned permanently once its quadlet file is deleted")
	}
	if !firstRemovePos.IsValid() {
		t.Fatal("runPodRemove no longer deletes any file; this test's premise needs revisiting")
	}
	if reapPos > firstRemovePos {
		t.Errorf("ensureContainersRemoved runs at %s, AFTER the first file deletion at %s — by then the unit is gone and a surviving container can never be reaped",
			fset.Position(reapPos), fset.Position(firstRemovePos))
	}
}
