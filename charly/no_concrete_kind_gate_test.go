package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoConcreteKindInKernel is the structural capstone of the kernel/plugin boundary law
// (Cutover N): the kernel (charly/*.go production code) must NOT consume a concrete-kind
// spec.<Kind> Go type. Every vocab kind (sidecar/agent/init/resource/distro) and every
// substrate value (local/android/pod/k8s/vm) resolves to a Resolved* envelope via its
// plugin's OpResolve (Cutovers D–M); the kernel reads the envelope, never the concrete
// kind. A re-introduced typed consumption (a spec.<Kind> field-read, a typed uf map, a
// typed pre-decode) trips this gate — the teeth arm that makes the law self-enforcing.
//
// It parses each non-test file's AST (comments excluded by parser) and fails on any
// `spec.<Kind>` selector for a concrete kind, but ONLY in files that import the sdk/spec
// package as `spec` (so a local variable named `spec` with an unrelated field cannot false
// positive). `Init` is intentionally omitted from the concrete set: the unrelated
// `ShellSpec.Init` field shares the name; the init de-type (Cutover F) is proven by the
// service-render / capabilities tests. Allowed and NOT flagged: the Resolved* envelopes
// (spec.ResolvedVm, …) and per-kind SUB-types the envelope carries (spec.VmSource,
// spec.LibvirtDomain, spec.Format, …) — those are E-envelope data, not the kind itself.
func TestNoConcreteKindInKernel(t *testing.T) {
	concreteKinds := map[string]bool{
		"Vm": true, "Pod": true, "K8s": true, "Local": true, "Android": true,
		"Distro": true, "Sidecar": true, "Agent": true, "Resource": true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var violations []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		astF, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		// Only consider files that actually import the sdk/spec package as `spec`.
		importsSpec := false
		for _, imp := range astF.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			var name string
			if imp.Name != nil {
				name = imp.Name.Name
			} else {
				name = path[strings.LastIndex(path, "/")+1:]
			}
			if path == "github.com/opencharly/sdk/spec" && name == "spec" {
				importsSpec = true
				break
			}
		}
		if !importsSpec {
			continue
		}
		ast.Inspect(astF, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || x.Name != "spec" {
				return true
			}
			if concreteKinds[sel.Sel.Name] {
				pos := fset.Position(sel.Pos())
				violations = append(violations, fmt.Sprintf("%s:%d: spec.%s", f, pos.Line, sel.Sel.Name))
			}
			return true
		})
	}
	if len(violations) > 0 {
		t.Fatalf("kernel/plugin boundary law violated — charly production code consumes a concrete-kind spec.<Kind> type; each kind must resolve to its Resolved* envelope via the plugin (Cutovers D–M):\n  %s", strings.Join(violations, "\n  "))
	}
}
