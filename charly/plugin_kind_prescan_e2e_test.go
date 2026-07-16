package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExternalKind_PrescanConnectDecode proves F4 END-TO-END: a `kind: examplekind` entity whose
// serving plugin is NOT compiled in is RECOGNIZED at parse (the prescan registers the declared
// kind word), the plugin is CONNECTED by the depth-0 pre-pass (connectDeclaredKindPlugins,
// re-entrancy-guarded), and runPluginKind decodes the body into uf.PluginKinds — ALL during a
// single LoadUnified, with NO infinite recursion (the connect re-loads the SAME project root that
// contains the kind node; the guard + the normalizeNodeInto defer break the cycle). The test
// COMPLETING is the re-entrancy proof. Builds the real candy/plugin-example-kind OOP, so it is
// -short-gated like the other reverse-channel e2es.
func TestExternalKind_PrescanConnectDecode(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds the external kind plugin binary (slow)")
	}
	charlyDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	srcCandy, err := filepath.Abs("../candy/plugin-example-kind")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcCandy, "go.mod")); err != nil {
		t.Fatalf("example kind plugin module not found at %s: %v", srcCandy, err)
	}

	// Stage the candy into a temp project (go.mod replace rewritten to the ABSOLUTE charly dir so
	// the host build resolves it from the temp location) + a root entity using `kind: examplekind`.
	dir := t.TempDir()
	dstCandy := filepath.Join(dir, "candy", "plugin-example-kind")
	if err := copyCandyFixReplace(srcCandy, dstCandy, charlyDir); err != nil {
		t.Fatalf("stage candy: %v", err)
	}
	requireUnversionedSource(t, dstCandy)
	rootYAML := `version: ` + LatestSchemaVersion().String() + `
discover:
    - path: candy
      recursive: true
my-example-kind:
    examplekind:
        marker: F4-KIND-MARK
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(rootYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	// The whole F4 path: prescan recognizes examplekind → connectDeclaredKindPlugins builds +
	// connects it (re-entrancy-guarded) → normalizeNodeInto/runPluginKind decodes the body.
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified must parse+decode a kind:examplekind entity via the F4 prescan+connect: %v", err)
	}
	byName, ok := uf.PluginKinds["examplekind"]
	if !ok {
		t.Fatalf("no uf.PluginKinds[examplekind] (the external kind did not decode); have kinds %v", pluginKindKeys(uf))
	}
	got, ok := byName["my-example-kind"]
	if !ok {
		t.Fatalf("kind entity my-example-kind not decoded; have %v", byName)
	}
	if !strings.Contains(string(got), "F4-KIND-MARK") {
		t.Fatalf("decoded body %s missing the round-tripped marker", got)
	}
}

// TestExternalKind_OpValidateRejectsInvalidBody proves F7/C8: a class:kind plugin declaring
// Validates=true serves a deep OpValidate check the host dispatches at load, and error-severity
// Diagnostics FAIL the load — beyond the static CUE input-def gate (which #ExamplekindInput's open
// `marker?: string` would pass). The sentinel marker "INVALID" trips plugin-example-kind's
// OpValidate. Builds the real plugin OOP, so -short-gated; reuses copyCandyFixReplace.
func TestExternalKind_OpValidateRejectsInvalidBody(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds the external kind plugin binary (slow)")
	}
	charlyDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	srcCandy, err := filepath.Abs("../candy/plugin-example-kind")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcCandy, "go.mod")); err != nil {
		t.Fatalf("example kind plugin module not found at %s: %v", srcCandy, err)
	}

	dir := t.TempDir()
	dstCandy := filepath.Join(dir, "candy", "plugin-example-kind")
	if err := copyCandyFixReplace(srcCandy, dstCandy, charlyDir); err != nil {
		t.Fatalf("stage candy: %v", err)
	}
	requireUnversionedSource(t, dstCandy)
	rootYAML := `version: ` + LatestSchemaVersion().String() + `
discover:
    - path: candy
      recursive: true
bad-kind:
    examplekind:
        marker: INVALID
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(rootYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = LoadUnified(dir)
	if err == nil {
		t.Fatal("LoadUnified must FAIL when the kind's OpValidate rejects the body (Diagnostics error)")
	}
	if !strings.Contains(err.Error(), "validation failed") || !strings.Contains(err.Error(), "INVALID") {
		t.Fatalf("error %q must name the OpValidate failure + the rejected marker", err)
	}
}

func pluginKindKeys(uf *UnifiedFile) []string {
	out := make([]string, 0, len(uf.PluginKinds))
	for k := range uf.PluginKinds {
		out = append(out, k)
	}
	return out
}

func requireUnversionedSource(t *testing.T, dir string) {
	t.Helper()
	if pluginSourceHasGitRevision(dir, pluginBuildEnv(os.Environ(), dir)) {
		t.Fatal("staged candy must remain an unversioned source fixture")
	}
}

// copyCandyFixReplace copies a candy module tree to dst, rewriting go.mod's
// `replace github.com/opencharly/sdk => ../../sdk` to the ABSOLUTE sdk dir (derived
// from charlyDir's parent) so buildPluginBinary resolves it from the temp project
// location.
func copyCandyFixReplace(src, dst, charlyDir string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if d.Name() == "go.mod" {
			sdkDir := filepath.Join(filepath.Dir(charlyDir), "sdk")
			var fixed []string
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "replace github.com/opencharly/sdk") {
					fixed = append(fixed, "replace github.com/opencharly/sdk => "+sdkDir)
					continue
				}
				fixed = append(fixed, line)
			}
			b = []byte(strings.Join(fixed, "\n"))
		}
		return os.WriteFile(target, b, 0o644)
	})
}
