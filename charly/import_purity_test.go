package main

// The IMPORT-PURITY + ZERO-ALIASES gate (P16, step 7 of #55's import-purity
// program) — the mechanical enforcement of CLAUDE.md's "Core is a PLUGIN HOST"
// standing rules, REPLACING the former file-allowlist gate
// (kernel_manifest_floor_test.go, deleted in this same change): rather than
// pinning charly/'s file SET, this gate asserts every file's IMPORT SURFACE
// directly. charly/ is the plugin HOST — it loads plugins, dispatches to
// plugins, and brokers the wire; it must never import an sdk MECHANISM KIT
// (kit/buildkit/deploykit/loaderkit/vmshared, nor the sdk root plugin-authoring
// package — those are for PLUGINS to import, never the host) and must carry no
// re-export alias file OR re-export alias FORM (W0: the ZERO-ALIASES gate was
// filename-glob-only — `*_aliases.go`/`*_alias.go` — which a re-export hiding in an
// innocuously-named file, or an inline `type X = spec.Y`/`var x = spec.Y`/`const x =
// spec.Y` top-level declaration anywhere in charly/, evaded entirely; gpu_shim.go and 8
// inline forms across 6 files did exactly that. TestNoAliasForms_ZeroPackageLevelReexports
// below closes the gap by matching the SHAPE, not the filename).
//
// The allowlist is DERIVED from the current, reviewed import set — not
// invented — and is a hard-fail floor: an import outside it, or ANY
// `github.com/opencharly/sdk` import (root or any subpackage), fails the gate
// with the offending file:line so the violation is immediately actionable.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// allowedThirdPartyPrefixes is the reviewed, exact-module allowlist of non-
// opencharly external dependencies charly/ (the plugin HOST) may import. Each
// entry is a module-root; the import itself or any of its subpackages
// (module/...) is allowed. Derived from the CURRENT charly/ import set — every
// entry below is exercised by real production or test code, reviewed here, not
// invented.
var allowedThirdPartyPrefixes = []string{
	"cuelang.org/go",                 // CUE ingress-schema validation (cue_*.go)
	"github.com/alecthomas/kong",     // the Kong CLI grammar (main.go, image.go, …)
	"github.com/hashicorp/go-hclog",  // go-plugin's structured logger
	"github.com/hashicorp/go-plugin", // the out-of-process plugin transport (gRPC broker)
	"golang.org/x/term",              // terminal-detection (commands.go isTerminal)
	"google.golang.org/grpc",         // the wire-broker gRPC transport (plugin_grpc.go, …)
	"gopkg.in/yaml.v3",               // comment-preserving *yaml.Node edits
}

// allowedOpencharlyPrefixes is the reviewed opencharly-module allowlist.
//   - github.com/opencharly/spec is the wire-type + Op-vocabulary contract every
//     host and plugin shares (the proto/plugin-api wire contract lives at
//     spec/proto, itself under this prefix) — the ONE opencharly dependency the
//     host mechanism code may reach for.
//   - github.com/opencharly/charly is THIS repo's own module tree: charly/ core
//     imports specific candy/plugin-* SIBLING MODULES ONLY for the B-bootstrap
//     compiled-in-plugin registry (plugins_generated.go, pluginsgen-generated,
//     reproducibility-gated) and the build-engine plugin-connect seam
//     (host_build_buildengine.go) — a same-repo registry-seed import, NOT an
//     sdk-mechanism-kit import, so it is its own reviewed exception, distinct
//     from (and never a loophole for) the sdk ban below.
//
// github.com/opencharly/sdk (root OR any subpackage — kit/buildkit/deploykit/
// loaderkit/vmshared) is NEVER allowed here: the sdk mechanism kits + the sdk
// root plugin-authoring package are for PLUGINS to import, never the host.
var allowedOpencharlyPrefixes = []string{
	"github.com/opencharly/spec",
	"github.com/opencharly/charly",
}

// forbiddenSDKPrefix names the ONE forbidden opencharly module — checked
// explicitly (ahead of the generic allowlist test) so a violation's failure
// message calls out the sdk import by name, rather than folding into the
// generic "not on the allowlist" message.
const forbiddenSDKPrefix = "github.com/opencharly/sdk"

// TestImportPurity_HostImportsOnlySpecAndVettedThirdParty walks every .go file
// in charly/ (prod AND test, package main plus internal/pluginsgen) and
// asserts its imports are all stdlib, the reviewed third-party allowlist, or
// github.com/opencharly/{spec,charly} — never github.com/opencharly/sdk.
func TestImportPurity_HostImportsOnlySpecAndVettedThirdParty(t *testing.T) {
	var sdkViolations []string
	var otherViolations []string

	err := filepath.Walk(".", func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base != "." && (base == "testdata" || strings.HasPrefix(base, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}

		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			line := fset.Position(imp.Pos()).Line

			if isStdlibImport(importPath) {
				continue
			}
			if importPath == forbiddenSDKPrefix || strings.HasPrefix(importPath, forbiddenSDKPrefix+"/") {
				sdkViolations = append(sdkViolations, fmt.Sprintf(
					"%s:%d: forbidden import %q — the sdk mechanism kits (and the sdk root plugin-authoring package) are for PLUGINS, never the host; charly/ core imports spec/* + proto only",
					path, line, importPath))
				continue
			}
			if hasAllowedPrefix(importPath, allowedOpencharlyPrefixes) || hasAllowedPrefix(importPath, allowedThirdPartyPrefixes) {
				continue
			}
			otherViolations = append(otherViolations, fmt.Sprintf(
				"%s:%d: import %q is not on the reviewed allowlist (add it to allowedThirdPartyPrefixes/allowedOpencharlyPrefixes with a justification, or remove the import)",
				path, line, importPath))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk charly/ for import purity: %v", err)
	}

	sort.Strings(sdkViolations)
	sort.Strings(otherViolations)

	if len(sdkViolations) > 0 {
		t.Errorf("IMPORT-PURITY gate: %d forbidden github.com/opencharly/sdk import(s) in charly/ core:\n%s",
			len(sdkViolations), strings.Join(sdkViolations, "\n"))
	}
	if len(otherViolations) > 0 {
		t.Errorf("IMPORT-PURITY gate: %d import(s) outside the reviewed allowlist:\n%s",
			len(otherViolations), strings.Join(otherViolations, "\n"))
	}
}

// isStdlibImport reports whether importPath is a standard-library import: its
// first path segment carries no dot (every non-stdlib module root is a
// registrable domain, which always contains a dot — cuelang.org, github.com,
// golang.org, google.golang.org, gopkg.in, …).
func isStdlibImport(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".")
}

// hasAllowedPrefix reports whether importPath equals one of prefixes, or is a
// subpackage of one (prefix + "/...").
func hasAllowedPrefix(importPath string, prefixes []string) bool {
	for _, p := range prefixes {
		if importPath == p || strings.HasPrefix(importPath, p+"/") {
			return true
		}
	}
	return false
}

// TestZeroAliases_NoAliasFilesInCharlyCore is the standing v2 ZERO-ALIASES
// gate, absorbed into this rewrite: no charly/*_aliases.go (or a stray
// *_alias.go) re-export file may exist. An alias means the call site is
// mislocated — the fix is MOVING the call site into its owning plugin, never
// re-exporting it. Currently vacuously green (no alias files remain); the
// assertion keeps it that way.
func TestZeroAliases_NoAliasFilesInCharlyCore(t *testing.T) {
	var found []string
	for _, pattern := range []string{"*_aliases.go", "*_alias.go"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		found = append(found, matches...)
	}
	if len(found) == 0 {
		return
	}
	sort.Strings(found)
	t.Errorf("ZERO-ALIASES gate: %d alias file(s) present in charly/ core — an alias means the call site is mislocated; move it into its owning plugin, never re-export it:\n%s",
		len(found), strings.Join(found, "\n"))
}

// aliasFormTargetImportPaths are the imports this gate polices: github.com/opencharly/spec/spec
// (the shared wire-TYPE vocabulary — Config/CalVer/RunMode/CredentialHealth/CheckEnv/…, the
// surface the 8 pre-W0 inline forms and gpu_shim.go's filename-evading forms all re-exported),
// plus github.com/opencharly/spec/ops (the Op* TRANSPORT-PROTOCOL vocabulary) and
// github.com/opencharly/spec/schema (the embedded CUE schema FS) — K5 W4 closes the W0-registered
// IOU that carved those two out: `const OpRun = ops.OpRun` (provider.go, ~258 call sites) and
// `var schemaFS = sdkschema.FS` (cue_schema.go, 2 call sites) are the EXACT same re-export shape
// TestNoAliasForms_ZeroPackageLevelReexports already catches for spec/spec, just spelled with
// `const`/pointed at a sibling spec/* package — there was never a genuine wire-contract reason to
// exempt them (both are plain intra-module renames, unlike the sdk Handshake/proto family, which
// IS the real plugin-API wire contract and stays untouched by this gate). schemaFS is fully
// repointed (0 remaining call sites). The Op* repoint is NOT fully closed: OpRun/OpValidate/
// OpEmit/OpResolve remain aliased in provider.go because their only bare-form consumers are 4
// hard-fenced files in another teammate's active tranche (service_render.go,
// substrate_template_resolve.go, preempt.go, check_kit_adapter.go) this cutover cannot touch —
// see provider.go's own comment. aliasFormKnownIOUs below is the ONE narrow, auditable exemption
// for exactly those 4 declarations; every other charly/*.go re-export of spec/spec, spec/ops, or
// spec/schema fails the gate. Narrowing by RESOLVED IMPORT PATH (not the qualifier's local name)
// so a differently-aliased import can't dodge the gate either.
var aliasFormTargetImportPaths = []string{
	"github.com/opencharly/spec/spec",
	"github.com/opencharly/spec/ops",
	"github.com/opencharly/spec/schema",
}

// aliasFormKnownIOUs is the ONE reviewed, narrow exemption this gate honors: a "file.go:Name"
// key for a top-level re-export the gate would otherwise flag, kept ONLY because its remaining
// bare-form consumers are hard-fenced files another teammate is actively working in (see
// provider.go's comment for the full rationale). Every entry here is a REGISTERED IOU, not a
// permanent exception — whoever next lands a commit touching any of those 4 fenced files deletes
// both the provider.go const and its matching entry here in the SAME commit.
var aliasFormKnownIOUs = map[string]bool{
	"provider.go:OpRun":      true,
	"provider.go:OpValidate": true,
	"provider.go:OpEmit":     true,
	"provider.go:OpResolve":  true,
}

// TestNoAliasForms_ZeroPackageLevelReexports is the FORM-based half of the ZERO-ALIASES gate
// (W0): TestZeroAliases_NoAliasFilesInCharlyCore only catches a re-export FILE named
// *_aliases.go/*_alias.go; it says nothing about a re-export hiding in an innocuously-named
// file (gpu_shim.go) or an inline top-level `type X = spec.Y` / `var x = spec.Y` / `const x =
// spec.Y` declaration anywhere in charly/*.go (8 such forms across 6 files, pre-W0). This test
// parses every non-test charly/*.go file's AST and fails on any top-level type/var/const
// declaration whose RHS is EXACTLY a bare selector into aliasFormTargetImportPath — never a call
// (`spec.F()`), a composite literal, or a closure, which are legitimate package-level bindings,
// not re-export aliases. Grouped declarations (`const ( A = spec.X\n B = spec.Y )`) are walked
// per-ValueSpec, so the RunModeLive/RunModeBox shape (grouped, not single-line) is caught too.
// Test-file-local aliases (substrate_spec_aliases_test.go) stay allowed — they exist ONLY to let
// TestNoConcreteKindInKernel assert PRODUCTION code holds no concrete-kind type; a test fixture
// binding is not a re-export residue.
// aliasFormTargetQualifiers resolves astF's imports to the set of local qualifier names bound to
// any of aliasFormTargetImportPaths, so the gate follows a renamed import (`import myspec
// "github.com/opencharly/spec/spec"`) and ignores an unrelated package merely named "spec" by
// coincidence.
func aliasFormTargetQualifiers(astF *ast.File) map[string]bool {
	qualifiers := map[string]bool{}
	for _, imp := range astF.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		targeted := false
		for _, target := range aliasFormTargetImportPaths {
			if path == target {
				targeted = true
				break
			}
		}
		if !targeted {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		qualifiers[name] = true
	}
	return qualifiers
}

// aliasFormViolationsInSpec inspects one type/var/const ValueSpec/TypeSpec for the re-export
// alias shape (`type X = q.Y` / `var x = q.Y` / `const x = q.Y` where q is a target-package
// qualifier), skipping any (file, name) pair registered in aliasFormKnownIOUs. Returns the
// formatted violation string, or "" if spec isn't a violation.
func aliasFormViolationInSpec(f string, fset *token.FileSet, tok token.Token, spec ast.Spec, qualifiers map[string]bool) string {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		// `type X = spec.Y` (an ALIAS decl — s.Assign is set) whose RHS is a bare selector into
		// the target package. `type X spec.Y` (a DEFINITION, s.Assign unset — a genuinely new
		// named type, not a re-export) is NOT flagged.
		if !s.Assign.IsValid() {
			return ""
		}
		sel, ok := s.Type.(*ast.SelectorExpr)
		if !ok {
			return ""
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok || !qualifiers[x.Name] || aliasFormKnownIOUs[f+":"+s.Name.Name] {
			return ""
		}
		pos := fset.Position(s.Pos())
		return fmt.Sprintf("%s:%d: type %s = %s.%s", f, pos.Line, s.Name.Name, x.Name, sel.Sel.Name)
	case *ast.ValueSpec:
		if len(s.Names) != 1 || len(s.Values) != 1 {
			return "" // multi-name/multi-value specs are never a bare re-export shape
		}
		sel, ok := s.Values[0].(*ast.SelectorExpr)
		if !ok {
			return "" // a call, composite literal, or closure — a legitimate binding
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok || !qualifiers[x.Name] || aliasFormKnownIOUs[f+":"+s.Names[0].Name] {
			return ""
		}
		pos := fset.Position(s.Pos())
		kw := "var"
		if tok == token.CONST {
			kw = "const"
		}
		return fmt.Sprintf("%s:%d: %s %s = %s.%s", f, pos.Line, kw, s.Names[0].Name, x.Name, sel.Sel.Name)
	}
	return ""
}

func TestNoAliasForms_ZeroPackageLevelReexports(t *testing.T) {
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
		astF, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}

		qualifiers := aliasFormTargetQualifiers(astF)
		if len(qualifiers) == 0 {
			continue // file doesn't import any target package at all
		}

		for _, decl := range astF.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || (gd.Tok != token.TYPE && gd.Tok != token.VAR && gd.Tok != token.CONST) {
				continue
			}
			for _, spec := range gd.Specs {
				if v := aliasFormViolationInSpec(f, fset, gd.Tok, spec, qualifiers); v != "" {
					violations = append(violations, v)
				}
			}
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("ZERO-ALIASES form gate: %d inline re-export alias form(s) of %s in charly/ core — a bare `type X = spec.Y` / `var x = spec.Y` / `const x = spec.Y` top-level declaration means the call site is mislocated; repoint every caller to spec.Y directly and delete the alias, never re-export it (add a NARROW, reviewed entry to aliasFormKnownIOUs only for a genuine registered cross-wave IOU, never as a routine escape hatch):\n%s",
			len(violations), strings.Join(aliasFormTargetImportPaths, ", "), strings.Join(violations, "\n"))
	}
}
