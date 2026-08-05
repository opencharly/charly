package main

// TestNoKindSwitchInKernel is a W5-terminus CI tells-test (task #16) enforcing the
// kernel/plugin boundary law's core clause (CLAUDE.md "The kernel/plugin boundary
// law"): "a kind-word switch... is an incomplete seam". It complements the narrower,
// pre-existing TestNoSubstrateWordSwitchInDeployConsult (no_substrate_word_switch_gate_test.go,
// P9) — that gate only catches a bare `.Target == "word"` BinaryExpr over 5 substrate
// words across charly/+sdk/deploykit; THIS gate catches the broader STRUCTURAL shape —
// an actual `switch` statement or an if/else-if chain of >=3 arms — dispatching on ANY
// of the full deploy-substrate vocabulary (pod/vm/k8s/local/android, plus the entity
// words group/candy/deploy/bundle), scoped to charly/ core only (the kernel).
//
// A single stray `if x == "candy"` (kind-recognition Data — the loader/materialize
// files legitimately read pn.Disc == "candy" to route box-vs-layer parsing, per the
// boundary law's own carve-out for "kind-recognition Data") is NOT flagged: a lone
// if-arm is not a DISPATCH shape. The gate fires only when the code structurally
// resembles a multi-way kind dispatch: a switch with >=1 case matching the vocabulary
// (a switch already implies "compare one value against several"), or an if-chain with
// >=3 arms where at least one arm compares to a vocabulary word.
//
// Exemption mechanism: kindSwitchExemptions below, each entry a file:line + a
// justification string reviewed by the orchestrator. Expected EMPTY at authoring
// (W0/A3 already killed the known live violations, host_build_deploy_entity_resolve.go's
// kind-switch among them — that file no longer exists in this tree). A hit here is a
// FINDING to report to the program lead, never a silent allowlist add.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// kindSwitchVocabulary is the deploy-substrate + entity kind-word vocabulary this gate
// polices, per the W5 brief: the 5 deploy substrates plus the 4 entity/verb words whose
// accidental re-introduction as a dispatch key would also be a kernel/plugin boundary
// violation (group/candy/deploy/bundle).
var kindSwitchVocabulary = map[string]bool{
	"pod": true, "vm": true, "k8s": true, "local": true, "android": true,
	"group": true, "candy": true, "deploy": true, "bundle": true,
}

// kindSwitchExemptions is the reviewed, justified exemption list. Expected EMPTY.
// A future entry MUST carry "file:line" -> "one-line justification reviewed by the
// orchestrator" — never an unreviewed drive-by add.
var kindSwitchExemptions = map[string]string{}

// kindSwitchViolation is one detected switch/if-chain kind-word dispatch site.
type kindSwitchViolation struct {
	file string
	line int
	kind string // "switch" or "if-chain"
	word string
}

func (v kindSwitchViolation) String() string {
	return v.file + ":" + itoa(v.line) + ": " + v.kind + " dispatches on kind-word " + `"` + v.word + `"`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// kindSwitchViolationsInFile parses src (the content of the file named f) and returns
// every switch-statement or >=3-arm if-chain that dispatches on a kindSwitchVocabulary
// string literal. fset is shared across callers so reported positions are stable.
func kindSwitchViolationsInFile(fset *token.FileSet, f string, src []byte) ([]kindSwitchViolation, error) {
	astF, err := parser.ParseFile(fset, f, src, 0)
	if err != nil {
		return nil, err
	}

	var violations []kindSwitchViolation

	// caseLiteralWord extracts a bare string literal's value ("pod", not `x == "pod"`) —
	// the plain-value form a *ast.SwitchStmt case list uses.
	caseLiteralWord := func(e ast.Expr) (string, bool) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		w := strings.Trim(lit.Value, `"`)
		return w, kindSwitchVocabulary[w]
	}
	// eqlLiteralWord extracts the vocabulary word out of an `X == "word"` /
	// `"word" == X` BinaryExpr — the tag-less switch / if-condition comparison form.
	eqlLiteralWord := func(be *ast.BinaryExpr) (string, bool) {
		if be.Op != token.EQL {
			return "", false
		}
		if w, ok := caseLiteralWord(be.Y); ok {
			return w, true
		}
		if w, ok := caseLiteralWord(be.X); ok {
			return w, true
		}
		return "", false
	}

	ast.Inspect(astF, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.TypeSwitchStmt:
			return true // a type switch is never a kind-word STRING dispatch
		case *ast.SwitchStmt:
			for _, cc := range stmt.Body.List {
				clause, ok := cc.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range clause.List {
					var word string
					var hit bool
					if word, hit = caseLiteralWord(expr); !hit {
						if be, ok := expr.(*ast.BinaryExpr); ok {
							word, hit = eqlLiteralWord(be)
						}
					}
					if hit {
						pos := fset.Position(clause.Pos())
						violations = append(violations, kindSwitchViolation{f, pos.Line, "switch", word})
					}
				}
			}
		case *ast.IfStmt:
			// Only walk from the ROOT of a chain (an IfStmt that is not itself the
			// Else arm of a parent IfStmt) so a chain is counted/reported once.
			if isElseIfArm(astF, stmt) {
				return true
			}
			arms, words := ifChainArmsAndWords(stmt, eqlLiteralWord)
			if arms >= 3 && len(words) > 0 {
				pos := fset.Position(stmt.Pos())
				sorted := make([]string, 0, len(words))
				for w := range words {
					sorted = append(sorted, w)
				}
				sort.Strings(sorted)
				violations = append(violations, kindSwitchViolation{f, pos.Line, "if-chain", strings.Join(sorted, ",")})
			}
		}
		return true
	})
	return violations, nil
}

// isElseIfArm reports whether stmt is reached as the `else if` arm of some other
// *ast.IfStmt in astF — i.e. it is NOT the root of its chain. A full-file ast.Inspect
// visits every IfStmt including nested else-if arms, so the chain walker must skip
// non-root arms to avoid double-counting/reporting the same chain multiple times.
func isElseIfArm(astF *ast.File, target *ast.IfStmt) bool {
	found := false
	ast.Inspect(astF, func(n ast.Node) bool {
		if found {
			return false
		}
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		if elseIf, ok := ifs.Else.(*ast.IfStmt); ok && elseIf == target {
			found = true
			return false
		}
		return true
	})
	return found
}

// ifChainArmsAndWords walks an if/else-if/…/else chain from its root, returning the
// total arm count (if + every else-if + a trailing plain else, each counted once) and
// the set of kindSwitchVocabulary words compared via EQL anywhere in the chain's
// conditions.
func ifChainArmsAndWords(root *ast.IfStmt, eqlLiteralWord func(*ast.BinaryExpr) (string, bool)) (int, map[string]bool) {
	words := map[string]bool{}
	arms := 0
	cur := root
	for {
		arms++
		collectEQLWords(cur.Cond, eqlLiteralWord, words)
		switch next := cur.Else.(type) {
		case *ast.IfStmt:
			cur = next
			continue
		case *ast.BlockStmt:
			arms++ // trailing plain `else {}` counts as an arm too
		}
		break
	}
	return arms, words
}

// collectEQLWords walks cond (which may be an `a || b || c` OR-tree, or a single
// comparison) and records every kindSwitchVocabulary word found in an EQL comparison.
func collectEQLWords(cond ast.Expr, eqlLiteralWord func(*ast.BinaryExpr) (string, bool), out map[string]bool) {
	be, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return
	}
	if be.Op == token.LOR || be.Op == token.LAND {
		collectEQLWords(be.X, eqlLiteralWord, out)
		collectEQLWords(be.Y, eqlLiteralWord, out)
		return
	}
	if w, hit := eqlLiteralWord(be); hit {
		out[w] = true
	}
}

// TestNoKindSwitchInKernel walks every charly/*.go production file and fails if any
// switch statement or >=3-arm if-chain dispatches on a kindSwitchVocabulary word.
func TestNoKindSwitchInKernel(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var violations []kindSwitchViolation
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		vs, err := kindSwitchViolationsInFile(fset, f, src)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		violations = append(violations, vs...)
	}

	var unexempt []string
	for _, v := range violations {
		key := v.file + ":" + itoa(v.line)
		if _, ok := kindSwitchExemptions[key]; ok {
			continue
		}
		unexempt = append(unexempt, v.String())
	}
	if len(unexempt) > 0 {
		sort.Strings(unexempt)
		t.Errorf("kernel/plugin boundary law violated — %d kind-word switch/if-chain dispatch site(s) in charly/ core (an incomplete seam per CLAUDE.md's boundary law; move the behaviour to the plugin that owns the kind, dispatch via the registry/InvokeProvider instead):\n  %s",
			len(unexempt), strings.Join(unexempt, "\n  "))
	}
}

// TestNoKindSwitchInKernel_TeethProof proves the detector actually FIRES on a genuine
// violation shape (never touching a real charly/ file — parses an in-memory synthetic
// source instead, so there is nothing to revert before committing). Covers both
// detected shapes: a switch with a vocabulary case, and a 3-arm if-chain comparing to
// a vocabulary word.
func TestNoKindSwitchInKernel_TeethProof(t *testing.T) {
	const switchSrc = `package main

func dispatch(kind string) string {
	switch kind {
	case "pod":
		return "pod-path"
	case "widget":
		return "widget-path"
	}
	return ""
}
`
	const ifChainSrc = `package main

func dispatch(kind string) string {
	if kind == "vm" {
		return "vm-path"
	} else if kind == "widget" {
		return "widget-path"
	} else if kind == "gadget" {
		return "gadget-path"
	}
	return ""
}
`
	fset := token.NewFileSet()

	vs, err := kindSwitchViolationsInFile(fset, "synthetic_switch.go", []byte(switchSrc))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].kind != "switch" || vs[0].word != "pod" {
		t.Fatalf("teeth proof FAILED: switch-shape detector did not fire on a synthetic case %q dispatch; got %+v", "pod", vs)
	}
	t.Logf("teeth proof OK (switch): %s", vs[0].String())

	vs, err = kindSwitchViolationsInFile(fset, "synthetic_ifchain.go", []byte(ifChainSrc))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].kind != "if-chain" || vs[0].word != "vm" {
		t.Fatalf("teeth proof FAILED: if-chain-shape detector did not fire on a synthetic 3-arm chain comparing to %q; got %+v", "vm", vs)
	}
	t.Logf("teeth proof OK (if-chain): %s", vs[0].String())

	// Negative control: a lone if (not a chain) comparing to a vocabulary word must NOT
	// fire — this is the exact "kind-recognition Data" shape the boundary law allows
	// (e.g. `if pn.Disc == "candy"` in materialize.go/layers.go/provider_kind_invoke.go).
	const loneIfSrc = `package main

func classify(disc string) bool {
	if disc == "candy" {
		return true
	}
	return false
}
`
	vs, err = kindSwitchViolationsInFile(fset, "synthetic_loneif.go", []byte(loneIfSrc))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("teeth proof FAILED: a lone (non-chain) if must NOT trip the gate (kind-recognition Data is allowed); got %+v", vs)
	}
	t.Logf("teeth proof OK (lone if correctly NOT flagged)")
}
