package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
	"github.com/opencharly/spec/testkit"
)

// authoredMemberTree is the member subtree both beds author identically: two PEER pod members
// (web, cache), a NESTED pod-in-pod (cache→migrate), and a cross-member ${HOST:cache} check on web.
// The two beds' top-node KINDS differ: `examplestructkind:` (an external STRUCTURAL plugin kind)
// vs the POST-MIGRATE primary-substrate spelling (`pod:` primary + deploy-level siblings — the
// group-kind removal's target shape, Cutover C task 1; the former `group:` builtin baseline is
// gone with the kind). If the F5 authored-member input-threading is correct, the plugin bed
// reconstructs the SAME authored member tree the builtin loader folds for the migrated spelling.
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
// the core buildDeployNode recursion — the single member-decode source of truth) and threads them
// to the plugin's ops.OpLoad via op.Env; the plugin ATTACHES them to its spec.Deploy reply — so the host
// folds a COMPLETE Deploy (with the AUTHORED members) into uf.Deploy. The proof is BYTE-EQUIVALENCE
// to the builtin `group:` path: the SAME authored member tree under `examplestructkind:` and under
// `group:` must produce an IDENTICAL uf.Deploy entry (same peer/nested members, same hoisted
// cross-member plan, same deploy-config). This is the HIGHEST-risk F5 assumption (a plugin
// reconstructs the AUTHORED member tree, not a synthesized stand-in); it is the foundation for
// externalizing the seven builtin structural kind decoders (group first). Builds the real plugin
// OOP, so -short-gated. Reuses testkit.CopyCandyFixReplace.
func TestExternalStructKind_StructuralDecode(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds the external structural kind plugin binary (slow)")
	}
	charlyDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	srcCandy := pluginModuleDir(t, "example-structkind")
	if _, err := os.Stat(filepath.Join(srcCandy, "go.mod")); err != nil {
		t.Fatalf("example structkind plugin module not found at %s: %v", srcCandy, err)
	}
	ver := LatestSchemaVersion().String()

	// --- Bed 1: the EXTERNAL structural plugin kind (examplestructkind) ---
	pluginDir := t.TempDir()
	if err := testkit.CopyCandyFixReplace(srcCandy, filepath.Join(pluginDir, "candy", "plugin-example-structkind"), charlyDir); err != nil {
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

	// --- Bed 2: the POST-MIGRATE primary-substrate spelling — the equivalence baseline (no
	// plugin): the group-kind removal (Cutover C task 1) unrolls the former group: bed into
	// the FIRST member's substrate (pod, carrying web's body + the deploy-config scalars)
	// with the remaining member (cache) as a deploy-level sibling — the exact shape the
	// unroll-group-deploy migrate row writes. The plugin bed must reconstruct the same
	// member-decode truth the builtin loader folds for the migrated spelling. ---
	baseDir := t.TempDir()
	baseYAML := "version: " + ver + `
check-structkind-e2e:
    pod:
        disposable: true
        lifecycle: dev
        description: e2e authored-member reconstruction
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
	if err := os.WriteFile(filepath.Join(baseDir, "charly.yml"), []byte(baseYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	pluginUF, _, err := LoadUnified(pluginDir)
	if err != nil {
		t.Fatalf("LoadUnified must parse+decode a STRUCTURAL kind with AUTHORED members via F5: %v", err)
	}
	baseUF, _, err := LoadUnified(baseDir)
	if err != nil {
		t.Fatalf("LoadUnified post-migrate baseline: %v", err)
	}

	// F5: a STRUCTURAL kind folds into uf.Deploy (NOT uf.PluginKinds).
	dn, ok := pluginUF.Deploy["check-structkind-e2e"]
	if !ok {
		t.Fatalf("structural plugin kind not folded into uf.Deploy; have deploy keys %v", deployKeysFor(pluginUF))
	}
	if _, dup := pluginUF.PluginKinds["examplestructkind"]; dup {
		t.Fatal("structural kind also landed in uf.PluginKinds — it must be uf.Deploy ONLY")
	}
	base, ok := baseUF.Deploy["check-structkind-e2e"]
	if !ok {
		t.Fatalf("post-migrate baseline not folded into uf.Deploy; have %v", deployKeysFor(baseUF))
	}
	// The baseline's post-migrate shape: the FIRST member (web) is the PRIMARY (the
	// entity's own substrate node), cache the remaining deploy-level sibling.
	if base.Target != "pod" || base.Image != "coder" {
		t.Fatalf("baseline primary wrong: target=%q image=%q", base.Target, base.Image)
	}
	if len(base.Member) != 1 || base.MemberByName("cache") == nil {
		t.Fatalf("baseline members wrong: %+v", base.Member)
	}

	// The AUTHORED members were reconstructed (not empty, not synthesized): two peers + a nested.
	if len(dn.Member) != 2 || dn.MemberByName("web") == nil || dn.MemberByName("cache") == nil {
		t.Fatalf("authored peer members not reconstructed: %+v", dn.Member)
	}
	web := dn.MemberByName("web").Node
	if web.Image != "coder" || web.Target != "pod" {
		t.Fatalf("web member not reconstructed from authored input: %+v", web)
	}
	migrate := dn.MemberByName("cache").Node.MemberByName("migrate")
	if migrate == nil || migrate.Node == nil || migrate.Node.Image != "migrator" {
		t.Fatalf("nested authored member cache.migrate not reconstructed: %+v", dn.MemberByName("cache").Node)
	}
	// The cross-member ${HOST:cache} check survived input-threading (hoisted to the owner plan
	// with venue="web" by LoadUnified's generic member-plan hoist, same as the builtin path).
	if !strings.Contains(mustJSON(t, dn), "${HOST:cache}") {
		t.Fatalf("cross-member ${HOST:cache} check lost through input-threading: %s", mustJSON(t, dn))
	}

	// THE FOUNDATION PROOF: the external structural-plugin path and the builtin loader
	// fold the SAME authored member subtree IDENTICALLY — one member-decode source of
	// truth (R3). The plugin bed authors web+cache as members; the baseline's web body
	// IS its primary (the post-migrate spelling), so the shared comparable unit is the
	// cache member (the same authored subtree in both beds) plus the deploy-config
	// scalars the plugin maps into its reply.
	if got, want := mustJSON(t, dn.MemberByName("cache")), mustJSON(t, base.MemberByName("cache")); got != want {
		t.Fatalf("structural plugin member decode != builtin member decode\n plugin: %s\n base:   %s", got, want)
	}
	if (dn.Disposable == nil) != (base.Disposable == nil) || (dn.Disposable != nil && *dn.Disposable != *base.Disposable) {
		t.Fatalf("disposable scalar diverges: plugin %v vs base %v", dn.Disposable, base.Disposable)
	}
	if dn.Lifecycle != base.Lifecycle || dn.Description != base.Description {
		t.Fatalf("deploy-config scalars diverge: plugin (%q,%q) vs base (%q,%q)", dn.Lifecycle, dn.Description, base.Lifecycle, base.Description)
	}
	// The parse-guard FEED (Cutover C task 0, positive pin): the connected plugin's
	// REGISTERED input schema populates the declared-fields map.
	threaded := loaderThreaded()
	fields, ok := threaded.StructuralDeclaredFields["examplestructkind"]
	if !ok {
		t.Fatal("the connected external structural kind's input schema was not fed into StructuralDeclaredFields — the parse guard's parent-disc channel is inert for it")
	}
	for _, want := range []string{"marker", "disposable", "lifecycle", "description"} {
		if !fields[want] {
			t.Errorf("examplestructkind declared fields missing %q", want)
		}
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

func deployKeysFor(uf *spec.UnifiedFile) []string {
	out := make([]string, 0, len(uf.Deploy))
	for k := range uf.Deploy {
		out = append(out, k)
	}
	return out
}
