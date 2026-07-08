package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// authoredMemberTree is the member subtree both beds author identically: two PEER pod members
// (web, cache), a NESTED pod-in-pod (cache→migrate), and a cross-member ${HOST:cache} check on web.
// The ONLY difference between the two beds is the top-node KIND: `examplestructkind:` (an external
// STRUCTURAL plugin kind) vs `group:` (the builtin structural kind). If the F5 authored-member
// input-threading is correct, both fold to a byte-identical uf.Bundle entry.
const authoredMemberTree = `    web:
        pod:
            image: coder
            plan:
                - check: web reaches the cache
                  command: "redis-cli -h ${HOST:cache} ping"
    cache:
        pod:
            image: coder
        migrate:
            pod:
                image: migrator
                plan:
                    - check: migration ran
                      command: "test -f /done"
`

// TestExternalStructKind_StructuralDecode proves F5 authored-member INPUT-threading END-TO-END: a
// STRUCTURAL external kind (candy/plugin-example-structkind, NOT compiled in) is recognized +
// connected by the prescan; the host PRE-DECODES the node's AUTHORED resource-member children (via
// the core buildBundleNode recursion — the single member-decode source of truth) and threads them
// to the plugin's OpLoad via op.Env; the plugin ATTACHES them to its spec.Deploy reply — so the host
// folds a COMPLETE Bundle (with the AUTHORED members) into uf.Bundle. The proof is BYTE-EQUIVALENCE
// to the builtin `group:` path: the SAME authored member tree under `examplestructkind:` and under
// `group:` must produce an IDENTICAL uf.Bundle entry (same peer/nested members, same hoisted
// cross-member plan, same deploy-config). This is the HIGHEST-risk F5 assumption (a plugin
// reconstructs the AUTHORED member tree, not a synthesized stand-in); it is the foundation for
// externalizing the seven builtin structural kind decoders (group first). Builds the real plugin
// OOP, so -short-gated. Reuses copyCandyFixReplace from plugin_kind_prescan_e2e_test.go (same package).
func TestExternalStructKind_StructuralDecode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the external structural kind plugin binary (slow)")
	}
	charlyDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	srcCandy, err := filepath.Abs("../candy/plugin-example-structkind")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcCandy, "go.mod")); err != nil {
		t.Fatalf("example structkind plugin module not found at %s: %v", srcCandy, err)
	}
	ver := LatestSchemaVersion().String()

	// --- Bed 1: the EXTERNAL structural plugin kind (examplestructkind) ---
	pluginDir := t.TempDir()
	if err := copyCandyFixReplace(srcCandy, filepath.Join(pluginDir, "candy", "plugin-example-structkind"), charlyDir); err != nil {
		t.Fatalf("stage candy: %v", err)
	}
	// The deploy-config scalars (disposable/lifecycle/description) ride op.Params; the AUTHORED
	// members ride op.Env (host-pre-decoded, F5 input-threading) — the plugin attaches them.
	pluginYAML := "version: " + ver + `
discover:
    - path: candy
      recursive: true
check-structkind-e2e:
    examplestructkind:
        marker: spike
        disposable: true
        lifecycle: dev
        description: e2e authored-member reconstruction
` + authoredMemberTree
	if err := os.WriteFile(filepath.Join(pluginDir, "charly.yml"), []byte(pluginYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	// --- Bed 2: the BUILTIN structural kind (group) — the equivalence baseline (no plugin) ---
	groupDir := t.TempDir()
	groupYAML := "version: " + ver + `
check-structkind-e2e:
    group:
        disposable: true
        lifecycle: dev
        description: e2e authored-member reconstruction
` + authoredMemberTree
	if err := os.WriteFile(filepath.Join(groupDir, "charly.yml"), []byte(groupYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	pluginUF, _, err := LoadUnified(pluginDir)
	if err != nil {
		t.Fatalf("LoadUnified must parse+decode a STRUCTURAL kind with AUTHORED members via F5: %v", err)
	}
	groupUF, _, err := LoadUnified(groupDir)
	if err != nil {
		t.Fatalf("LoadUnified group baseline: %v", err)
	}

	// F5: a STRUCTURAL kind folds into uf.Bundle (NOT uf.PluginKinds).
	dn, ok := pluginUF.Bundle["check-structkind-e2e"]
	if !ok {
		t.Fatalf("structural plugin kind not folded into uf.Bundle; have bundle keys %v", bundleKeysFor(pluginUF))
	}
	if _, dup := pluginUF.PluginKinds["examplestructkind"]; dup {
		t.Fatal("structural kind also landed in uf.PluginKinds — it must be uf.Bundle ONLY")
	}
	base, ok := groupUF.Bundle["check-structkind-e2e"]
	if !ok {
		t.Fatalf("group baseline not folded into uf.Bundle; have %v", bundleKeysFor(groupUF))
	}

	// The AUTHORED members were reconstructed (not empty, not synthesized): two peers + a nested.
	if len(dn.Members) != 2 || dn.Members["web"] == nil || dn.Members["cache"] == nil {
		t.Fatalf("authored peer members not reconstructed: %+v", dn.Members)
	}
	if dn.Members["web"].Image != "coder" || dn.Members["web"].Target != "pod" {
		t.Fatalf("web member not reconstructed from authored input: %+v", dn.Members["web"])
	}
	if dn.Members["cache"].Children["migrate"] == nil || dn.Members["cache"].Children["migrate"].Image != "migrator" {
		t.Fatalf("nested authored member cache.migrate not reconstructed: %+v", dn.Members["cache"])
	}
	// The cross-member ${HOST:cache} check survived input-threading (hoisted to the owner plan
	// with venue="web" by LoadUnified's generic member-plan hoist, same as the builtin path).
	if !strings.Contains(mustJSON(t, dn), "${HOST:cache}") {
		t.Fatalf("cross-member ${HOST:cache} check lost through input-threading: %s", mustJSON(t, dn))
	}

	// THE FOUNDATION PROOF: the external structural-plugin path is BYTE-EQUIVALENT to the builtin
	// group path for the SAME authored member tree — one member-decode source of truth (R3).
	if got, want := mustJSON(t, dn), mustJSON(t, base); got != want {
		t.Fatalf("structural plugin decode != builtin group decode\n plugin: %s\n group:  %s", got, want)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func bundleKeysFor(uf *UnifiedFile) []string {
	out := make([]string, 0, len(uf.Bundle))
	for k := range uf.Bundle {
		out = append(out, k)
	}
	return out
}
