package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
	"github.com/opencharly/spec/testkit"
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
	if err := testkit.CopyCandyFixReplace(srcCandy, dstCandy, charlyDir); err != nil {
		t.Fatalf("stage candy: %v", err)
	}
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
// Validates=true serves a deep ops.OpValidate check the host dispatches at load, and error-severity
// Diagnostics FAIL the load — beyond the static CUE input-def gate (which #ExamplekindInput's open
// `marker?: string` would pass). The sentinel marker "INVALID" trips plugin-example-kind's
// ops.OpValidate. Builds the real plugin OOP, so -short-gated; reuses testkit.CopyCandyFixReplace.
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
	if err := testkit.CopyCandyFixReplace(srcCandy, dstCandy, charlyDir); err != nil {
		t.Fatalf("stage candy: %v", err)
	}
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
		t.Fatal("LoadUnified must FAIL when the kind's ops.OpValidate rejects the body (Diagnostics error)")
	}
	if !strings.Contains(err.Error(), "validation failed") || !strings.Contains(err.Error(), "INVALID") {
		t.Fatalf("error %q must name the ops.OpValidate failure + the rejected marker", err)
	}
}

func pluginKindKeys(uf *spec.UnifiedFile) []string {
	out := make([]string, 0, len(uf.PluginKinds))
	for k := range uf.PluginKinds {
		out = append(out, k)
	}
	return out
}
