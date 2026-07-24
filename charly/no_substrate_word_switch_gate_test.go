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

// TestNoSubstrateWordSwitchInDeployConsult is the P9 word-switch structural gate — the teeth
// that keep the descent/traits de-branching from regrowing. A DEPLOY CONSULT site (one that
// branches on HOW a substrate BEHAVES — which venue it uses, whether it is a machine, whether
// it is a chain leaf) must read the node's stamped #DeployTraits off node.Descent (via
// nodeTraits / deployTraitDescent / deployNodeVenue), NEVER a comparison `node.Target == "vm"`
// against a concrete substrate kind word (the kernel/plugin boundary law: a switch on a kind
// word is an incomplete seam). The DECLARED trait table lives in candy/plugin-substrate; the
// kernel consults it BY TRAIT.
//
// The gate parses each production file's AST and flags any binary comparison of a `.Target`
// selector against a concrete substrate-word string literal ("pod"/"vm"/"local"/"k8s"/
// "android"). It ALLOWLISTS the surfaces where reading the substrate WORD is legitimate and
// NOT a behaviour-branch (kernel-recognition Data or work the P9 contract deliberately did not
// touch): the loader/classifier (produces the word for dispatch), the validators (P13/P15 —
// they check the word is a recognized kind), and the status collectors (P14, not yet moved).
// A regrown `node.Target == "<substrate>"` behaviour-branch in any de-branched deploy/check
// consult file trips this gate.
func TestNoSubstrateWordSwitchInDeployConsult(t *testing.T) {
	substrateWords := map[string]bool{
		"pod": true, "vm": true, "local": true, "k8s": true, "android": true,
	}
	// Allowlisted files: reading the substrate WORD here is classification / validation /
	// status-reporting, NOT a substrate-behaviour branch. Keep this list tight — a new deploy
	// consult site does NOT belong here; it reads node.Descent traits instead.
	allowPrefix := []string{"status_", "validate"}
	allowExact := map[string]bool{
		// Loader / classifier: these PRODUCE the substrate word (dispatch classification,
		// kind-recognition Data), they do not branch on how the substrate behaves.
		"unified.go":         true,
		"node_normalize.go":  true,
		"node_bundle.go":     true,
		"deploy_nodeform.go": true,
		"bundle_add_cmd.go":  true, // `target` string dispatch (not `.Target`); classifyNodeTarget itself moved to deploykit.ClassifyNodeTarget (W4)
		"plugin_prescan.go":  true, // recognizedDeploySubstrate registry gate
		// deploykit lib: findVmDeploy reads the PERSISTED deploy state, where
		// node.Descent is stripped on save (deploy_state.go: "loader-DERIVED,
		// never operator-authored"), so the trait is unavailable and it must read
		// the persisted Target. Fully closing it needs Descent PERSISTED in the
		// deploy state — a state-schema cutover beyond P9 (tracked follow-up).
		"deploy_state.go": true,
	}
	allowed := func(f string) bool {
		if allowExact[f] {
			return true
		}
		for _, p := range allowPrefix {
			if strings.HasPrefix(f, p) {
				return true
			}
		}
		return false
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// P9: the gate ALSO covers the sdk/deploykit lib (the deploy Mechanism), not
	// just charly-core — the descent/traits de-branching spans both. A regrown
	// node.Target word-switch in the deploy chain/tree there must trip this too.
	dkFiles, err := filepath.Glob("../sdk/deploykit/*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, dkFiles...)
	fset := token.NewFileSet()
	var violations []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed(filepath.Base(f)) {
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
		ast.Inspect(astF, func(n ast.Node) bool {
			be, ok := n.(*ast.BinaryExpr)
			if !ok || (be.Op != token.EQL && be.Op != token.NEQ) {
				return true
			}
			// One operand a `.Target` selector, the other a substrate-word string literal.
			isTargetSel := func(e ast.Expr) bool {
				sel, ok := e.(*ast.SelectorExpr)
				return ok && sel.Sel.Name == "Target"
			}
			wordLit := func(e ast.Expr) (string, bool) {
				lit, ok := e.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return "", false
				}
				w := strings.Trim(lit.Value, `"`)
				return w, substrateWords[w]
			}
			var word string
			var hit bool
			if isTargetSel(be.X) {
				word, hit = wordLit(be.Y)
			} else if isTargetSel(be.Y) {
				word, hit = wordLit(be.X)
			}
			if hit {
				pos := fset.Position(be.Pos())
				violations = append(violations, fmt.Sprintf("%s:%d: .Target %s %q", f, pos.Line, be.Op, word))
			}
			return true
		})
	}
	if len(violations) > 0 {
		t.Fatalf("P9 word-switch gate violated — a deploy consult site branches on a concrete substrate kind word instead of reading node.Descent traits (nodeTraits/deployTraitDescent). Read the DECLARED #DeployTraits (venue/machine_venue/leaf_only/…) off the stamped descent; the trait table lives in candy/plugin-substrate:\n  %s", strings.Join(violations, "\n  "))
	}
}
