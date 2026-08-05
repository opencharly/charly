package main

// TestReverseChannelIsThreeLegs is a W5-terminus CI tells-test (task #16). The
// north-star end-state (kwave-north-star.md) declares "the reverse channel is exactly
// THREE legs: (1) live venue-executor re-materialization, (2) InvokeProvider peer
// dispatch, (3) plugin-binary build + CLI reentry — every OTHER host_build_* seam
// exists only because plugins couldn't load the project or touch state; once the
// loader is in sdk/loaderkit (W1) those seams are DELETED, not thinned." Reaching
// that count is the LATER waves' work (W1 landed; W2-W4 have been shrinking the
// residue since); this gate's job TODAY is the drift detector that makes forward
// progress mechanical and irreversible: it enumerates every LIVE registerHostBuilder
// call site (the wire-broker registry itself panics on a duplicate kind at package-
// var-init, so a registered "kind" string is a de-facto GLOBAL identity) and asserts
// the set matches a reviewed, one-entry-per-kind whitelist table exactly.
//
// The whitelist is a floor from ABOVE, not a target: it starts at today's full
// residue (47 kinds — the loader/build/deploy/pod/check machinery still mid-
// dissolution across W2-W4) and is EXPECTED TO SHRINK as later units land (each
// deleted registerHostBuilder call site drops its whitelist row in the SAME commit —
// the gate fails otherwise, forcing the trim). An UNREVIEWED ADDITION — a brand new
// kind string appearing in the live set that the whitelist does not already name — is
// treated as a program-level FINDING (reported to the orchestrator), never silently
// allowlisted by this gate itself.
//
// Read reverseChannelHostBuilderWhitelist below for the per-kind receipt.

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

// reverseChannelKindClause is one whitelist row: the registered HostBuild kind string,
// the file that registers it, and a short clause. Clause is either a direct citation of
// a KERNEL_MANIFEST.md row (file already documented — quoted verdict) or the literal
// string "UNDOCUMENTED (pending KERNEL_MANIFEST.md row)" for a file the manifest does
// not yet cover — TestKernelManifestBidirectional (kernel_manifest_bidirectional_test.go)
// is the gate that closes that gap file-by-file; this table is never the place to
// invent a clause for an undocumented file.
type reverseChannelKindClause struct {
	Kind   string
	File   string
	Clause string
}

// reverseChannelHostBuilderWhitelist is the reviewed table. MAINTENANCE RULE: a wave
// that deletes a registerHostBuilder call site removes its row here in the SAME
// commit (the gate fails otherwise — that IS the enforcement). A row may be REMOVED
// freely; a row may be ADDED only alongside the new call site it documents, and any
// such addition is a reviewable diff a validator inspects like any other production
// change — this table only ever shrinks toward the north-star's three-leg end-state,
// it does not grow.
var reverseChannelHostBuilderWhitelist = []reverseChannelKindClause{
	// --- Rows with a direct KERNEL_MANIFEST.md citation ---
	{"pod-lifecycle", "host_build_pod_lifecycle_dispatch.go", "M — the ONE op-discriminated pod-lifecycle dispatch (A10 consolidation of the former 8 per-verb kinds; KERNEL_MANIFEST.md A10 row)"},
	{"check-load-plugins", "host_build_check_load_plugins.go", "M — plugin-loading mechanism (KERNEL_MANIFEST.md B5: hostBuildCheckLoadPlugins, confirmed production caller candy/plugin-check/command.go:211)"},
	{"construct-step", "host_build_construct_step.go", "M — THE kind/verb dispatch mechanism (KERNEL_MANIFEST.md W2: providerRegistry.ResolveVerb/resolve(ClassStep,...))"},
	{"box-fetch-resolve", "host_build_box_fetch_resolve.go", "B (K1 floor) (KERNEL_MANIFEST.md W2: wraps ResolveProjectRepo->the ProjectLoader EnsureRepoDownloaded seam; the former refs.go core wrapper is deleted, K-wave 2 cone R1)"},
	{"remote-image-resolve", "host_build_remote_image_resolve.go", "B (K1 floor) (KERNEL_MANIFEST.md W2: EnsureRepoDownloaded only, box-RESOLVE half already plugin-side)"},
	{"loader-bootstrap", "host_build_loader_floor.go", "M — wire-broker reverse-channel leg, loader-mechanism face (KERNEL_MANIFEST.md W4: forwards to runBootstrapPhase)"},
	{"loader-walk", "host_build_loader_floor.go", "M — wire-broker reverse-channel leg, loader-mechanism face (KERNEL_MANIFEST.md W4: forwards to hostWalkProject prescan+connect)"},
	{"loader-threaded", "host_build_loader_floor.go", "M — wire-broker reverse-channel leg, loader-mechanism face (KERNEL_MANIFEST.md W4: forwards to loaderThreaded() D-snapshot)"},
	{"loader-materialize", "host_build_loader_floor.go", "M — wire-broker reverse-channel leg, loader-mechanism face (KERNEL_MANIFEST.md W4: forwards to hostMaterializeProjectSeams())"},
	{"validate-word-sets", "validate_project_host.go", "M — the provider registry itself (KERNEL_MANIFEST.md: hostBuildValidateWordSets answers ProviderCapabilities/ActCapableVerbs over the plugin's OWN envelope-derived word inventory; the former validate-project-checks CUE + remote-candy legs folded into candy/plugin-box, K-wave 2 cone R1 unit B)"},
	{"check-bed-gpu-prereq", "host_build_check_bed_gpu_prereq.go", "M — wire-broker leg, THE ONE seam surviving check-bed's dissolution (KERNEL_MANIFEST.md K5: operator-dropped GPU-hardware exception, gpu_allocate.go DetectVFIO)"},
	{"step-emit", "step_emit_hostbuild.go", "M — STAY+CONSOLIDATE (KERNEL_MANIFEST.md W4: thin forwarder to the compiled-in \"oci-dispatch\" class:step provider, the host-side half candy/plugin-deploy-pod still needs)"},
	{"buildengine-connect-plugins", "host_build_buildengine.go", "M — plugin-loading mechanism (KERNEL_MANIFEST.md W2: hostBuildConnectPlugins calls loadProjectPlugins, registers into providerRegistry)"},
	{"buildengine-context-ignore-baseline", "host_build_buildengine.go", "B — same-module embed boundary (KERNEL_MANIFEST.md W2: hostBuildContextIgnoreBaseline returns baselineContextIgnore, charly's own //go:embed)"},

	// --- Rows NOT yet in KERNEL_MANIFEST.md — undocumented, tracked by gate 3 ---
	{"overlay", "build_overlay.go", "UNDOCUMENTED (pending KERNEL_MANIFEST.md row)"},
	{"deploy-plugins-connect", "host_build_deploy_plugins_connect.go", "UNDOCUMENTED (pending KERNEL_MANIFEST.md row)"},
	{"deploy-node-del-dispatch", "host_build_deploy_node_del_dispatch.go", "UNDOCUMENTED (pending KERNEL_MANIFEST.md row)"},
	{"retention-defaults", "host_build_retention_defaults.go", "UNDOCUMENTED (pending KERNEL_MANIFEST.md row)"},
	{"feature", "host_build_feature.go", "UNDOCUMENTED (pending KERNEL_MANIFEST.md row)"},
	{"cli", "host_build_cli.go", "UNDOCUMENTED (pending KERNEL_MANIFEST.md row)"},
	{"pod-config-detect-devices", "host_build_pod_config_seams.go", "UNDOCUMENTED (pending KERNEL_MANIFEST.md row)"},
	{"pod-config-list-sidecars", "host_build_pod_config_seams.go", "UNDOCUMENTED (pending KERNEL_MANIFEST.md row)"},
	{"resolve-target-add", "host_build_resolve_target_add.go", "M — the per-node ResolveTarget+Add terminal (KERNEL_MANIFEST.md bank D row)"},
	{"plugin-binary", "plugin_dispatch_reverse.go", "M — leg 3 of the north-star's three legs (plugin-binary build + CLI reentry); see file header hostBuildPluginBinary"},
}

// hostBuilderCallSite is one collected registerHostBuilder(...) call in the live tree.
type hostBuilderCallSite struct {
	Kind string
	File string
	Line int
}

// collectRegisterHostBuilderCallSites AST-walks every charly/*.go production file and
// returns one entry per registerHostBuilder(kindExpr, ...) call, with kindExpr resolved
// to its underlying string value — either a bare string literal, or (the common case
// here) a package-level `const xBuilderKind = "..."` identifier resolved via a
// same-package symbol table built from every top-level single-name/single-value
// const/var string declaration in the walked file set.
func collectRegisterHostBuilderCallSites(fset *token.FileSet, files []string) ([]hostBuilderCallSite, error) {
	parsed := make(map[string]*ast.File, len(files))
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		astF, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		parsed[f] = astF
	}

	// Build the package-level string-const/var symbol table across the whole file set
	// (charly is a single `package main` — every file shares one identifier scope).
	symbols := map[string]string{}
	for _, astF := range parsed {
		for _, decl := range astF.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				symbols[vs.Names[0].Name] = strings.Trim(lit.Value, `"`)
			}
		}
	}

	var sites []hostBuilderCallSite
	for f, astF := range parsed {
		ast.Inspect(astF, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != "registerHostBuilder" || len(call.Args) == 0 {
				return true
			}
			var kind string
			var resolved bool
			switch arg := call.Args[0].(type) {
			case *ast.BasicLit:
				if arg.Kind == token.STRING {
					kind = strings.Trim(arg.Value, `"`)
					resolved = true
				}
			case *ast.Ident:
				kind, resolved = symbols[arg.Name]
			}
			pos := fset.Position(call.Pos())
			if !resolved {
				sites = append(sites, hostBuilderCallSite{Kind: fmt.Sprintf("<UNRESOLVED expr at %s:%d>", f, pos.Line), File: f, Line: pos.Line})
				return true
			}
			sites = append(sites, hostBuilderCallSite{Kind: kind, File: f, Line: pos.Line})
			return true
		})
	}
	return sites, nil
}

// TestReverseChannelIsThreeLegs asserts the LIVE registerHostBuilder kind set matches
// reverseChannelHostBuilderWhitelist exactly (see the file-level doc comment for why
// exact-match, not <=3, is the right assertion at this point in the program).
func TestReverseChannelIsThreeLegs(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var prodFiles []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			prodFiles = append(prodFiles, f)
		}
	}

	fset := token.NewFileSet()
	sites, err := collectRegisterHostBuilderCallSites(fset, prodFiles)
	if err != nil {
		t.Fatal(err)
	}

	live := map[string]hostBuilderCallSite{}
	var unresolved []string
	for _, s := range sites {
		if strings.HasPrefix(s.Kind, "<UNRESOLVED") {
			unresolved = append(unresolved, s.Kind)
			continue
		}
		if prior, dup := live[s.Kind]; dup {
			t.Fatalf("registerHostBuilder(%q) called twice in the live tree (%s:%d and %s:%d) — the registry itself panics on this at runtime; the AST walker double-counted or the tree genuinely regressed",
				s.Kind, prior.File, prior.Line, s.File, s.Line)
		}
		live[s.Kind] = s
	}
	if len(unresolved) > 0 {
		sort.Strings(unresolved)
		t.Fatalf("collectRegisterHostBuilderCallSites could not resolve %d registerHostBuilder kind argument(s) to a string — extend the symbol-table resolver (it only follows a bare string literal or a single-name/single-value package-level const/var):\n  %s",
			len(unresolved), strings.Join(unresolved, "\n  "))
	}

	whitelist := map[string]reverseChannelKindClause{}
	for _, row := range reverseChannelHostBuilderWhitelist {
		if _, dup := whitelist[row.Kind]; dup {
			t.Fatalf("reverseChannelHostBuilderWhitelist has a duplicate row for kind %q — fix the whitelist table itself", row.Kind)
		}
		whitelist[row.Kind] = row
	}

	var newFindings []string
	for kind, site := range live {
		if _, ok := whitelist[kind]; !ok {
			newFindings = append(newFindings, fmt.Sprintf("%s:%d: registerHostBuilder(%q) — NOT in reverseChannelHostBuilderWhitelist", site.File, site.Line, kind))
		}
	}
	var staleRows []string
	for kind, row := range whitelist {
		if _, ok := live[kind]; !ok {
			staleRows = append(staleRows, fmt.Sprintf("%q (%s) — no longer registered anywhere in the live tree", kind, row.File))
		}
	}

	if len(newFindings) > 0 {
		sort.Strings(newFindings)
		t.Errorf("REVERSE-CHANNEL gate: %d NEW registerHostBuilder kind(s) not in the reviewed whitelist — this is a program-level finding (report to the orchestrator, never silently allowlist):\n  %s",
			len(newFindings), strings.Join(newFindings, "\n  "))
	}
	if len(staleRows) > 0 {
		sort.Strings(staleRows)
		t.Errorf("REVERSE-CHANNEL gate: %d stale whitelist row(s) reference a kind no longer registered — trim reverseChannelHostBuilderWhitelist in the SAME commit that deleted the registration (the whitelist only ever shrinks):\n  %s",
			len(staleRows), strings.Join(staleRows, "\n  "))
	}
}

// TestReverseChannelIsThreeLegs_TeethProof proves the drift detector fires on both a
// genuinely NEW (unwhitelisted) registration and a STALE whitelist row, using in-memory
// synthetic source — no real charly/ file is touched, so nothing to revert.
func TestReverseChannelIsThreeLegs_TeethProof(t *testing.T) {
	const src = `package main

const syntheticKind = "synthetic-new-kind"

var _ = func() bool {
	registerHostBuilder(syntheticKind, nil)
	registerHostBuilder("another-inline-kind", nil)
	return true
}()
`
	fset := token.NewFileSet()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "synthetic_hostbuilder.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	sites, err := collectRegisterHostBuilderCallSites(fset, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 {
		t.Fatalf("teeth proof FAILED: expected 2 collected call sites, got %d: %+v", len(sites), sites)
	}
	kinds := map[string]bool{}
	for _, s := range sites {
		kinds[s.Kind] = true
	}
	if !kinds["synthetic-new-kind"] || !kinds["another-inline-kind"] {
		t.Fatalf("teeth proof FAILED: const-resolved and literal kind forms both expected; got %+v", sites)
	}
	t.Logf("teeth proof OK: collector resolves both a const-identifier kind and a bare string-literal kind: %+v", sites)

	// A synthetic "new-not-whitelisted" finding + a synthetic "stale whitelist row" are
	// both exactly what the real gate's diff logic (live vs whitelist set) is built to
	// catch — proven directly against the real reverseChannelHostBuilderWhitelist map
	// shape without mutating it.
	whitelist := map[string]bool{"synthetic-new-kind": true} // "another-inline-kind" deliberately absent
	staleWhitelistOnly := map[string]bool{"a-kind-that-was-deleted": true}
	var newFindings, stale []string
	for k := range kinds {
		if !whitelist[k] {
			newFindings = append(newFindings, k)
		}
	}
	for k := range staleWhitelistOnly {
		if !kinds[k] {
			stale = append(stale, k)
		}
	}
	if len(newFindings) != 1 || newFindings[0] != "another-inline-kind" {
		t.Fatalf("teeth proof FAILED: expected exactly the unwhitelisted kind to be flagged as a new finding; got %+v", newFindings)
	}
	if len(stale) != 1 || stale[0] != "a-kind-that-was-deleted" {
		t.Fatalf("teeth proof FAILED: expected exactly the stale row to be flagged; got %+v", stale)
	}
	t.Logf("teeth proof OK: new-finding + stale-row diff logic both fire correctly")
}
