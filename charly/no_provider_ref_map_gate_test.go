package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestNoProviderRefMapInKernel is the C5 structural gate — the teeth that keep the
// "which plugin serves which word" fact out of charly's kernel. That fact is kind-recognition
// Data (boundary-law clause D): it lives in each plugin's OWN repo (`plugin.providers:` +
// `plugin.source:`) and reaches core only through the GENERATED provider-ref index
// (pluginProviderRefs, emitted by pluginsgen into plugins_refs_generated.go). A hand-written
// per-word/per-kind map or a candy-ref string literal in production code is BY DEFINITION an
// R-item that leaked — the exact crutch this cutover removed (the former
// externalDeploySubstratePlugins kernel map + the single-verb vmPluginCandyRef helper, both
// deleted).
//
// The gate flags two shapes in production (.go, non-_test) source:
//
//  1. a candy-ref literal — a string containing `github.com/opencharly/` … `/candy/` — EXCEPT
//     inside the generated index file (plugins_refs_generated.go), which IS the projection.
//  2. a `map[string]string` composite literal whose keys are reserved-word capability strings
//     (`<class>:<word>`) — a hand-written word→ref table.
//
// Expected-empty exemption list: a genuine, justified exception must be named here WITH its
// reason. Adding one is a boundary-law decision (a validator rejects an unexplained entry).
func TestNoProviderRefMapInKernel(t *testing.T) {
	// Files that are ALLOWED to carry the shape, each with its reason.
	allowExact := map[string]string{
		// plugins_refs_generated.go IS the generated projection — the one legitimate home
		// of the word→ref fact in the charly tree. It is regenerated, never hand-edited.
		"plugins_refs_generated.go": "the GENERATED provider-ref index (pluginsgen output)",
		// plugins_generated.go is the GENERATED compiled-in registration: it imports each
		// compiled-in plugin candy's Go module path (the module path is a Go import, not a
		// word→ref DATA table). Regenerated, never hand-edited.
		"plugins_generated.go": "the GENERATED compiled-in plugin registration (pluginsgen output)",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, ok := allowExact[name]; ok {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.BasicLit:
				if v.Kind == token.STRING {
					s := strings.Trim(v.Value, "`\"")
					if strings.Contains(s, "github.com/opencharly/") && strings.Contains(s, "/candy/") {
						offenders = append(offenders, name+": candy-ref literal "+v.Value)
					}
				}
			case *ast.CompositeLit:
				// A map[string]string whose keys are capability strings ("<class>:<word>").
				if !isStringKeyedMap(v) {
					return true
				}
				for _, elt := range v.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.BasicLit)
					if !ok || key.Kind != token.STRING {
						continue
					}
					if capabilityKeyRE(strings.Trim(key.Value, "`\"")) {
						offenders = append(offenders, name+": word→ref map key "+key.Value)
					}
				}
			}
			return true
		})
	}
	if len(offenders) > 0 {
		t.Errorf("charly kernel carries a word→provider fact (boundary-law clause D — it belongs in the plugin repo, reached via the GENERATED index):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// isStringKeyedMap reports whether a composite literal is a `map[string]<...>` — the shape a
// hand-written word→ref table takes. (A struct literal or a slice literal is not it.)
func isStringKeyedMap(v *ast.CompositeLit) bool {
	if v.Type == nil {
		return false
	}
	mt, ok := v.Type.(*ast.MapType)
	if !ok {
		return false
	}
	key, ok := mt.Key.(*ast.Ident)
	return ok && key.Name == "string"
}

// capabilityKeyRE reports whether s looks like a reserved-word capability key
// ("<class>:<word>" or the command three-segment form) — the key a word→ref map would use.
func capabilityKeyRE(s string) bool {
	i := strings.IndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return false
	}
	class := ProviderClass(s[:i])
	return providerClasses[class]
}
