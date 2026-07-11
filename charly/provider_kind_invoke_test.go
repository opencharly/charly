package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// e3KindTestProv is a fake EXTERNAL kind provider (a Provider that is NOT a
// KindProvider — it has no typed DecodeNode, only the Invoke envelope), used to
// prove the E3 kind-class out-of-proc path without standing up a real gRPC plugin.
// Its Invoke(OpLoad) echoes the validated value as the entity.
type e3KindTestProv struct{}

func (e3KindTestProv) Reserved() string     { return "e3kind" }
func (e3KindTestProv) Class() ProviderClass { return ClassKind }
func (e3KindTestProv) Invoke(_ context.Context, op *Operation) (*Result, error) {
	return &Result{JSON: op.Params}, nil
}

// TestRunPluginKind_DecodesViaEnvelope proves E3-kind: an external plugin kind is
// (1) RECOGNIZED by the loader (classifyDisc's registry check — fails "no
// discriminator" without it), (2) validated against its served .cue
// (validateAuthoredPluginInput), and (3) decoded via the Invoke envelope
// (runPluginKind) into uf.PluginKinds — the kind-class generalization of the verb
// dual-path. Built-in kinds keep the typed DecodeNode fast path (no JSON); this is
// the external (out-of-proc-capable) path. The whole test FAILS without the E3-kind
// host implementation.
func TestRunPluginKind_DecodesViaEnvelope(t *testing.T) {
	RegisterBuiltinProvider(e3KindTestProv{})
	if err := registerPluginUnitSchema("e3kind-test", PluginSchema{
		CueSource: "#E3kindInput: {name: string & !=\"\"}\n",
		InputDefs: map[string]string{provKey(ClassKind, "e3kind"): "#E3kindInput"},
	}); err != nil {
		t.Fatalf("register schema: %v", err)
	}
	doc := "myk:\n  e3kind:\n    name: hello\n"
	var ydoc yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &ydoc); err != nil {
		t.Fatal(err)
	}
	nodes, err := genericNodesFromDoc(&ydoc)
	if err != nil {
		t.Fatalf("parse rejected the plugin kind (loader-recognition gap): %v", err)
	}
	uf := &UnifiedFile{}
	for _, gn := range nodes {
		if err := normalizeNodeInto(gn, uf); err != nil {
			t.Fatalf("normalizeNodeInto: %v", err)
		}
	}
	// Name-keyed storage: the entity is stored under its node name ("myk").
	if got := uf.PluginKinds["e3kind"]; len(got) != 1 || !strings.Contains(string(got["myk"]), "hello") {
		t.Fatalf("expected 1 e3kind entity named 'myk' containing 'hello', got %v", got)
	}
}

// TestMergePluginKindsMap_NameKeyedOverride proves Cutover A's root-wins override on
// the merge itself: uf.PluginKinds is kind→name→body, and merging a source that
// authors the SAME kind+name as the destination yields ONE entry — the destination
// (root/project) wins and the source (embedded/import) is dropped — exactly the
// build-vocab map merge (mergeDistroMap) rule. A new name in the source is gap-filled. (The
// pre-cutover append semantics would have produced two entries for the shared name.)
func TestMergePluginKindsMap_NameKeyedOverride(t *testing.T) {
	dst := map[string]map[string]json.RawMessage{
		"sidecar": {"tailscale": json.RawMessage(`{"image":"project"}`)},
	}
	src := map[string]map[string]json.RawMessage{
		"sidecar": {
			"tailscale": json.RawMessage(`{"image":"embedded"}`), // same name — must NOT override dst
			"redis":     json.RawMessage(`{"image":"embedded"}`), // new name — must be gap-filled
		},
	}
	mergePluginKindsMap(&dst, src)

	sc := dst["sidecar"]
	if len(sc) != 2 {
		t.Fatalf("expected 2 sidecar entries (tailscale override + redis gap-fill), got %d (%v)", len(sc), sc)
	}
	if got := string(sc["tailscale"]); got != `{"image":"project"}` {
		t.Errorf("tailscale not root-wins: got %q, want the project (dst) body", got)
	}
	if got := string(sc["redis"]); got != `{"image":"embedded"}` {
		t.Errorf("redis gap-fill missing/wrong: got %q", got)
	}
}
