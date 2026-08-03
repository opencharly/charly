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
// re-export alias file.
//
// The allowlist is DERIVED from the current, reviewed import set — not
// invented — and is a hard-fail floor: an import outside it, or ANY
// `github.com/opencharly/sdk` import (root or any subpackage), fails the gate
// with the offending file:line so the violation is immediately actionable.

import (
	"fmt"
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
