package main

// The PER-PLUGIN MINIMAL-IMPORT gate (P16, step 7's leg (b)) — Rule 2 of the
// project's plugin-authoring contract: a plugin candy (candy/plugin-*) must
// not DRAG a direct go.mod require on github.com/opencharly/{sdk,spec} that
// its own code never actually imports. This is deliberately NOT a byte-perfect
// `go mod tidy` mirror (that would also flag legitimate indirect-only churn
// unrelated to this contract) — it checks exactly the two opencharly modules
// this program cares about, and only DIRECT requires (an "// indirect" require
// is dependency-manager-owned, not a plugin author's declared dependency).
//
// This is explicitly NOT a ban on a plugin importing sdk — "plugins MAY import
// sdk" is the 4-layer model (a plugin genuinely using sdk.Serve/sdk/kit/etc is
// completely legitimate); the gate only catches the case where go.mod DECLARES
// a direct dependency the plugin's own source tree never actually imports.

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// candyModules are the two opencharly modules this gate tracks per plugin.
var candyModules = []string{
	"github.com/opencharly/sdk",
	"github.com/opencharly/spec",
}

// directRequireRE matches a go.mod require line for one of candyModules that
// is NOT marked "// indirect" — e.g. "	github.com/opencharly/sdk v0.0.0" but
// not "	github.com/opencharly/sdk v0.0.0 // indirect". Works for both the
// bare `require module version` form and a line inside a `require (...)`
// block (go.mod formatting is identical either way).
var directRequireRE = regexp.MustCompile(`^\s*(github\.com/opencharly/(?:sdk|spec))\s+v\S+\s*$`)

// TestPluginImportHygiene_NoUnusedOpencharlyModuleDeps walks every candy/*
// directory carrying its own go.mod, and for each direct require on
// github.com/opencharly/{sdk,spec}, verifies the candy's own Go source tree
// actually imports that module (or a subpackage of it) somewhere. A direct
// require with zero corresponding import is a dragged, unused dependency.
func TestPluginImportHygiene_NoUnusedOpencharlyModuleDeps(t *testing.T) {
	candyRoot := filepath.Join("..", "candy")
	entries, err := os.ReadDir(candyRoot)
	if err != nil {
		t.Fatalf("read %s: %v", candyRoot, err)
	}

	var violations []string
	var checked int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candyDir := filepath.Join(candyRoot, e.Name())
		goModPath := filepath.Join(candyDir, "go.mod")
		modBytes, readErr := os.ReadFile(goModPath)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue // not a Go module — nothing to check
			}
			t.Fatalf("read %s: %v", goModPath, readErr)
		}
		checked++

		directlyRequired := map[string]bool{}
		for _, line := range strings.Split(string(modBytes), "\n") {
			if strings.Contains(line, "// indirect") {
				continue
			}
			if m := directRequireRE.FindStringSubmatch(line); m != nil {
				directlyRequired[m[1]] = true
			}
		}
		if len(directlyRequired) == 0 {
			continue
		}

		used, walkErr := candyModuleUsage(candyDir)
		if walkErr != nil {
			t.Fatalf("scan imports under %s: %v", candyDir, walkErr)
		}

		var missing []string
		for _, mod := range candyModules {
			if directlyRequired[mod] && !used[mod] {
				missing = append(missing, mod)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			violations = append(violations, fmt.Sprintf(
				"%s: go.mod directly requires %s but no .go file under %s imports it (or a subpackage) — drop the unused require, or add the import that needs it",
				goModPath, strings.Join(missing, ", "), candyDir))
		}
	}

	if checked == 0 {
		t.Fatal("found zero candy/* modules with a go.mod — the candy tree layout changed; update candyRoot")
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("PER-PLUGIN MINIMAL-IMPORT gate: %d candy module(s) with an unused direct github.com/opencharly/{sdk,spec} dependency:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// candyModuleUsage recursively scans every .go file under dir (a candy's
// module root — including nested packages like cmd/serve, which share the
// module) and reports, for each of candyModules, whether ANY file imports it
// or one of its subpackages.
func candyModuleUsage(dir string) (map[string]bool, error) {
	used := map[string]bool{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base != filepath.Base(dir) && (base == "testdata" || strings.HasPrefix(base, ".")) {
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
			for _, mod := range candyModules {
				if importPath == mod || strings.HasPrefix(importPath, mod+"/") {
					used[mod] = true
				}
			}
		}
		return nil
	})
	return used, err
}
