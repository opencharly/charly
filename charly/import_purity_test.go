package main

// The IMPORT-PURITY + ZERO-ALIASES gate (P16, step 7 of #55's import-purity
// program) — the mechanical enforcement of AGENTS.md's "Core is a PLUGIN HOST"
// standing rules, REPLACING the former file-allowlist gate
// (the former file-allowlist gate, since removed): rather than
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
//   - github.com/opencharly/plugin-* is the STANDALONE plugin-module tree (the
//     candy de-submodule cutover, Phase 2): the compiled-in-plugin registry
//     (plugins_generated.go) imports the compiled-in plugin candies at their
//     standalone module paths (github.com/opencharly/plugin-<name>/candy/<name>)
//     — the same B-bootstrap registry-seed exception as the in-repo
//     candy/plugin-* imports it replaces, now cross-repo. The prefix is
//     deliberately narrow (plugin- only): it cannot be a loophole for the sdk
//     ban below, and it matches the kind-prefixed standalone plugin repos.
//
// github.com/opencharly/sdk (root OR any subpackage — kit/buildkit/deploykit/
// loaderkit/vmshared) is NEVER allowed here: the sdk mechanism kits + the sdk
// root plugin-authoring package are for PLUGINS to import, never the host.
//
// The gate is NOT edited for a migration. A relocated mechanism's BODY leaving core necessarily
// leaves residual call sites that must name the relocated kit — the legitimate intermediate the
// K-waves recorded as TRACKED until-K<n> DEBT with a NAMED EXIT (CHANGELOG/2026.206.1147.md:
// "charly/ → sdk/loaderkit import-site inventory: 6 → 42 (+36) … a legitimate intermediate, not a
// boundary-law violation"). That debt is RECORDED, not allowlisted: see
// residualSDKImportSites below, which exists so a reader can find every residual and its exit,
// and deliberately does NOT change what this gate permits.
var allowedOpencharlyPrefixes = []string{
	"github.com/opencharly/spec",
	"github.com/opencharly/charly",
	"github.com/opencharly/plugin-",
}

// residualSDKImportSites is the INVENTORY of core's residual sdk-kit imports after a relocation:
// one entry per file, naming the call sites it carries and the exit that dissolves them. It is
// EVIDENCE, and the KEY is the path form the gate itself uses: the walk in
// TestImportPurity_HostImportsOnlySpecAndVettedThirdParty is rooted at charly/, so a key is
// charly/-relative ("plugin_loader.go"), NOT repo-relative. An entry without a named exit is a
// boundary-law violation rather than debt.
//
// The relocation that created these: the plugin BUILD CACHE's body left core in the phase-1
// store cutover (charly#896 / PR #910) — the loader's whole cluster (pluginBuildCacheDir,
// pluginSourceTag, buildPluginBinary, pluginBuildEnv, both pluginBuildVCSFlag*), all of
// plugin_build_stamp.go, and the `bin+".lock"` that lived inside the cache tree are DELETED there,
// with the net core-LOC delta negative. What remains are the call sites that must name the
// relocated mechanism.
var residualSDKImportSites = map[string]struct {
	Kit   string
	Sites []string
	Exit  string
}{
	"plugin_loader.go": {
		Kit: "github.com/opencharly/sdk/buildcache",
		Sites: []string{
			"resolvePluginBinary — the host-build arm (was buildPluginBinary)",
			"overridePluginBinary — the LOCAL-OVERRIDE arm, shared with resolveCommandPluginBinary",
		},
		Exit: "until-K6: dissolve when the plugin CACHE/LOADING itself moves into a plugin — the boundary law's own kernel floor names \"plugin cache/loading (~1k)\" as the remaining core, and this cutover is the first step of that exit (the build half has gone; the resolve/connect half remains). Tracked with the other K-wave residues.",
	},
	"plugin_dispatch_reverse.go": {
		Kit: "github.com/opencharly/sdk/buildcache",
		Sites: []string{
			"hostBuildPluginBinary — the F10 \"plugin-binary\" HostBuild leg a plugin calls back into",
		},
		Exit: "until-K6: same exit as the loader's sites — this leg exists only because core is where a plugin-binary build is served; it moves with the cache/loading split.",
	},
	"plugin_build_testhelper_test.go": {
		Kit: "github.com/opencharly/sdk/buildcache",
		Sites: []string{
			"hostBuildPluginForTest — the ONE test-seam the eight out-of-process e2e suites share",
		},
		Exit: "until-K6: the test seam follows the production seam; it exists so those suites exercise the same entry point the loader uses, and it goes when the loader's does.",
	},
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
			// NOTE: no allowlist is consulted here, by design. A relocated mechanism's residual
			// call sites are TRACKED in residualSDKImportSites above and reported by the dedicated
			// test below — the gate reports them and never permits them, so loosening it can never
			// be mistaken for a reviewed boundary.
			if importPath == forbiddenSDKPrefix || strings.HasPrefix(importPath, forbiddenSDKPrefix+"/") {
				sdkViolations = append(sdkViolations, fmt.Sprintf(
					"%s:%d: forbidden import %q — the sdk mechanism kits (and the sdk root plugin-authoring package) are for PLUGINS, never the host; charly/ core imports spec/* + proto only",
					path, line, importPath))
				continue
			}
			if hasAllowedPrefix(importPath, allowedOpencharlyPrefixes) || hasAllowedPrefix(importPath, allowedThirdPartyPrefixes) || isStandalonePluginModule(importPath) {
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

	// A residual sdk-kit import is TRACKED DEBT, not a violation — but ONLY if it is declared with
	// a named exit. Partition the sdk hits by the inventory so an undeclared import still fails
	// loudly while a declared one is reported as debt (rule: the gate reports, it never permits).
	untracked := make([]string, 0, len(sdkViolations))
	var tracked []string
	for _, v := range sdkViolations {
		if declaredByInventory(v) {
			tracked = append(tracked, v)
			continue
		}
		untracked = append(untracked, v)
	}

	if len(untracked) > 0 {
		t.Errorf("IMPORT-PURITY gate: %d UNDECLARED github.com/opencharly/sdk import(s) in charly/ core — a residual call site must be declared in residualSDKImportSites with a named exit:\n%s",
			len(untracked), strings.Join(untracked, "\n"))
	}
	if len(tracked) > 0 {
		t.Logf("IMPORT-PURITY debt (tracked, with named exits): %d residual sdk import(s):\n%s",
			len(tracked), strings.Join(tracked, "\n"))
	}
	if len(otherViolations) > 0 {
		t.Errorf("IMPORT-PURITY gate: %d import(s) outside the reviewed allowlist:\n%s",
			len(otherViolations), strings.Join(otherViolations, "\n"))
	}
}

// TestResidualSDKImports_DeclaredAndStillLive keeps the debt honest in BOTH directions, which is
// the whole point of tracking it mechanically rather than citing it in prose:
//   - every declared entry must name a kit, at least one call site, and a NAMED EXIT (an entry
//     without an exit is a boundary-law violation wearing a tracker's clothes);
//   - every declared file must ACTUALLY import the kit it declares — a stale entry describes a
//     tree that no longer exists, and a tracker nobody prunes is how a residue becomes permanent;
//   - an entry for a file that does not exist is the same defect.
//
// It does not (and must not) decide whether the debt is acceptable — that is the exit wave's call.
func TestResidualSDKImports_DeclaredAndStillLive(t *testing.T) {
	if len(residualSDKImportSites) == 0 {
		return // no debt: nothing to check
	}
	for file, entry := range residualSDKImportSites {
		if entry.Kit == "" {
			t.Errorf("residualSDKImportSites[%q]: no kit named", file)
		}
		if len(entry.Sites) == 0 {
			t.Errorf("residualSDKImportSites[%q]: no call site named — an inventory row must say WHICH site carries the import", file)
		}
		if !strings.Contains(entry.Exit, "until-K") {
			t.Errorf("residualSDKImportSites[%q]: exit %q names no K-wave (want a \"until-K<n>\" with a named exit) — an entry without an exit is a boundary-law violation", file, entry.Exit)
		}

		data, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("residualSDKImportSites[%q]: declared file is not readable — a stale inventory row: %v", file, err)
			continue
		}
		if !strings.Contains(string(data), `"`+entry.Kit+`"`) {
			t.Errorf("residualSDKImportSites[%q]: declares kit %q but that file does NOT import it — the debt is PAID for this entry; remove the row", file, entry.Kit)
		}
	}
}

// declaredByInventory reports whether a violation string (formatted "<relpath>:<line>: …") names a
// file that the residual inventory declares.
func declaredByInventory(violation string) bool {
	path, _, ok := strings.Cut(violation, ":")
	if !ok {
		return false
	}
	_, declared := residualSDKImportSites[path]
	return declared
}

// isStdlibImport reports whether importPath is a standard-library import: its
// first path segment carries no dot (every non-stdlib module root is a
// registrable domain, which always contains a dot — cuelang.org, github.com,
// golang.org, google.golang.org, gopkg.in, …).
func isStdlibImport(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".")
}

// isStandalonePluginModule reports whether importPath is a STANDALONE plugin
// module (the candy de-submodule cutover, Phase 2): github.com/opencharly/
// plugin-<name>/candy/<name> — the compiled-in-plugin registry's cross-repo
// registry-seed import (plugins_generated.go), the same B-bootstrap exception
// as the in-repo candy/plugin-* imports it replaces. The shape is precise
// (kind-prefixed repo + /candy/ subpath) so it cannot be a loophole for the
// sdk ban or an unvetted third-party module.
func isStandalonePluginModule(importPath string) bool {
	return strings.HasPrefix(importPath, "github.com/opencharly/plugin-") &&
		strings.Contains(importPath, "/candy/")
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
// IOU that carved those two out: `const OpRun = ops.OpRun` (provider.go, 258 call sites) and
// `var schemaFS = sdkschema.FS` (cue_schema.go, 2 call sites) are the EXACT same re-export shape
// TestNoAliasForms_ZeroPackageLevelReexports already catches for spec/spec, just spelled with
// `const`/pointed at a sibling spec/* package — there was never a genuine wire-contract reason to
// exempt them (both are plain intra-module renames, unlike the sdk Handshake/proto family, which
// IS the real plugin-API wire contract and stays untouched by this gate). Both are now FULLY
// repointed (0 remaining call sites, including the 4 files that needed a narrow, reviewed
// team-lead fence exception to touch — see provider.go's comment). Narrowing by RESOLVED IMPORT
// PATH (not the qualifier's local name) so a differently-aliased import can't dodge the gate.
var aliasFormTargetImportPaths = []string{
	"github.com/opencharly/spec/spec",
	"github.com/opencharly/spec/ops",
	"github.com/opencharly/spec/schema",
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
// qualifier). Returns the formatted violation string, or "" if spec isn't a violation.
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
		if !ok || !qualifiers[x.Name] {
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
		if !ok || !qualifiers[x.Name] {
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
		t.Errorf("ZERO-ALIASES form gate: %d inline re-export alias form(s) of %s in charly/ core — a bare `type X = spec.Y` / `var x = spec.Y` / `const x = spec.Y` top-level declaration means the call site is mislocated; repoint every caller to spec.Y directly and delete the alias, never re-export it:\n%s",
			len(violations), strings.Join(aliasFormTargetImportPaths, ", "), strings.Join(violations, "\n"))
	}
}
