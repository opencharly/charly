package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// materialize_seam_parity_test.go — the K1-unit-1 byte-equivalence gate: proves the
// spec.Materializer-routed materialize pipeline (materialize.go/node_parsed.go/loader_threaded.go
// dispatching per node into candy/plugin-loader, which calls back through the DecodeEntity/
// BuildFleetEntity seam into the UNCHANGED clause-M dispatch, provider_kind_invoke.go) produces a
// project structurally and byte-identical to itself across independent loads of the SAME real,
// large fixture (box/fedora's charly.yml — 46 boxes, 116 candies, the full document loop +
// discovered-manifest fold + embedded-defaults merge + namespace recursion this cutover's
// orchestration drives). A silent materialize divergence — a node folded into the wrong map, a
// dropped entity, non-deterministic map iteration leaking into output — would show up here as a
// diff between two otherwise-identical loads.
//
// This is the R10-adjacent differential proof for the move: since R5 hard-cutover replaced
// normalizeNodeInto in place (no dual-mode old/new code retained to diff against directly), the
// proof is TWO independent real loads producing byte-identical entity maps — the strongest
// available evidence the new seam-routed dispatch is deterministic and correct on a real,
// non-trivial fixture. TestCandyKind_BothShapesByteEquivalent / TestSubstrateKind_BothShapesByteEquivalent
// (plugin_candy_kind_e2e_test.go / plugin_substrate_e2e_test.go) complement this at the single-kind
// level (plugin fold vs. a direct core baseline decode); TestPluginInstallstepEnvelopeParity
// complements it at the whole-envelope level (BakedMetadata/EmitTasks byte-identical against the
// SAME box/fedora fixture, transitively exercising this same materialize path).
func TestMaterializeSeam_RealFixtureDeterministic(t *testing.T) {
	t.Cleanup(snapshotProviderState())

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot = filepath.Dir(repoRoot) // charly/ -> repo root
	dir := filepath.Join(repoRoot, "box", "fedora")

	uf1, ok1, err1 := LoadUnified(dir)
	if err1 != nil || !ok1 {
		t.Fatalf("first LoadUnified(%s): ok=%v err=%v", dir, ok1, err1)
	}
	uf2, ok2, err2 := LoadUnified(dir)
	if err2 != nil || !ok2 {
		t.Fatalf("second LoadUnified(%s): ok=%v err=%v", dir, ok2, err2)
	}

	// Sanity: the seam-routed dispatch actually populated real data, not empty maps (a silently
	// no-op'd Materializer would still return ok=true with everything empty).
	// box/fedora vendors NO local candies (every candy is a remote @github ref, resolved later
	// by ScanAllCandyWithConfigOpts — not by LoadUnified's discover: fold), so only Box is
	// asserted non-empty here.
	if len(uf1.Box) == 0 {
		t.Fatal("uf1.Box is empty — the Materializer seam did not fold any images")
	}
	if _, ok := uf1.BoxConfig("fedora"); !ok {
		t.Fatal(`box "fedora" not present after materialize — expected entity missing`)
	}

	// Byte-equivalence across the two independent loads, field by field, for every map the
	// Materializer/dispatch chain writes: Box/Candy/Fleet (dedicated fields) + PluginKinds (which
	// ALSO now carries the 5 standalone-substrate-TEMPLATE kinds vm/pod/k8s/local/android — K1
	// unit-1 follow-up, foldStandaloneTemplateReply's generic fold, node_normalize.go). PluginKinds
	// is json:"-" on spec.UnifiedFile so it's compared explicitly, not via a whole-struct marshal which
	// would silently skip it.
	fields := []struct {
		name string
		a, b any
	}{
		{"Box", uf1.Box, uf2.Box},
		{"Candy", uf1.Candy, uf2.Candy},
		{"Fleet", uf1.Fleet, uf2.Fleet},
		{"PluginKinds", uf1.PluginKinds, uf2.PluginKinds},
	}
	for _, f := range fields {
		aj, err := json.Marshal(f.a)
		if err != nil {
			t.Fatalf("marshal uf1.%s: %v", f.name, err)
		}
		bj, err := json.Marshal(f.b)
		if err != nil {
			t.Fatalf("marshal uf2.%s: %v", f.name, err)
		}
		if string(aj) != string(bj) {
			t.Fatalf("materialize non-determinism on field %s: two independent loads of the same fixture diverged\n load1: %s\n load2: %s", f.name, aj, bj)
		}
	}
}
