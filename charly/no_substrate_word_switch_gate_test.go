package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoSubstrateWordSwitchInDeployConsult is the P9 word-switch structural gate — the teeth
// that keep the descent/traits de-branching from regrowing. A DEPLOY CONSULT site (one that
// branches on HOW a substrate BEHAVES — which venue it uses, whether it is a machine, whether
// it is a chain leaf) must read the node's stamped #DeployTraits off node.Descent (via
// nodeTraits / deployTraitDescent / nodeDescentVenue), NEVER a comparison `node.Target == "vm"`
// against a concrete substrate kind word (the kernel/plugin boundary law: a switch on a kind
// word is an incomplete seam). The DECLARED trait table lives in candy/plugin-substrate; the
// kernel consults it BY TRAIT.
//
// The gate parses each production file's AST and flags any binary comparison of a `.Target`
// selector against a concrete substrate-word string literal ("pod"/"vm"/"local"/"kubernetes"/
// "android"). It ALLOWLISTS the surfaces where reading the substrate WORD is legitimate and
// NOT a behaviour-branch (kernel-recognition Data or work the P9 contract deliberately did not
// touch): the loader/classifier (produces the word for dispatch), the validators (P13/P15 —
// they check the word is a recognized kind), and the status collectors (P14, not yet moved).
// A regrown `node.Target == "<substrate>"` behaviour-branch in any de-branched deploy/check
// consult file trips this gate.
func TestNoSubstrateWordSwitchInDeployConsult(t *testing.T) {
	substrateWords := map[string]bool{
		"pod": true, "vm": true, "local": true, "kubernetes": true, "android": true, "kindcluster": true, "kubevirt": true,
	}
	// Allowlisted files: reading the substrate WORD here is classification / validation /
	// status-reporting, NOT a substrate-behaviour branch. Keep this list tight — a new deploy
	// consult site does NOT belong here; it reads node.Descent traits instead.
	allowPrefix := []string{"status_", "validate"}
	allowExact := map[string]bool{
		// Loader / classifier: these PRODUCE the substrate word (dispatch classification,
		// kind-recognition Data), they do not branch on how the substrate behaves.
		// (node_normalize.go's classify wrapper was inlined into provider_kind_invoke.go and the
		// file deleted with the genericNode bridge, parser consolidation F2.2 — the classifier it
		// wrapped is the loaderkit IsStandaloneResourceKind reached through the loader seam, a
		// data consult, not a behaviour branch.)
		"deploy_nodeform.go": true,
		"deploy_add_cmd.go":  true, // `target` string dispatch (not `.Target`); classifyNodeTarget itself moved to deploykit.ClassifyNodeTarget (W4)
		"plugin_prescan.go":  true, // recognizedDeploySubstrate registry gate
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
	// The sdk is a PROXY-RESOLVED module since the sdk de-submodule cutover (there
	// is no in-tree `../sdk` directory), so the source is located through the
	// build's own module resolution — a stale `../sdk` glob would match NOTHING and
	// silently drop the gate's sdk coverage (which is exactly how the C6
	// deploy_state.go allowance rotted into a no-op).
	dkDir := sdkDeploykitSourceDir(t)
	dkFiles, err := filepath.Glob(filepath.Join(dkDir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dkFiles) == 0 {
		t.Fatalf("gate: no sdk/deploykit sources found under %q — the gate must not silently pass with zero sdk coverage", dkDir)
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

// sdkDeploykitSourceDir locates the deploykit package of the sdk contract module
// this build resolves — the module cache dir for the pinned proxy version, or a
// local directory when go.mod replaces the module (go list -m reports the
// effective dir in both cases). The former `../sdk/deploykit` glob assumed an
// in-tree sdk submodule; after the sdk de-submodule cutover that path no longer
// exists, so the glob matched nothing and the gate silently lost its coverage of
// the deploy Mechanism. A gate that scans nothing must not pass: every failure
// path here is a t.Fatal.
func sdkDeploykitSourceDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/opencharly/sdk").Output()
	if err != nil {
		t.Fatalf("gate: locating the sdk module source (go list -m github.com/opencharly/sdk): %v", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatal("gate: go list -m github.com/opencharly/sdk returned an empty dir — the gate must not silently pass")
	}
	return filepath.Join(dir, "deploykit")
}
