package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
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
	t.Cleanup(snapshotProviderState())
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
	uf := &spec.UnifiedFile{}
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

// TestMergePluginKindsMap_NameKeyedOverride relocated to sdk/loaderkit/merge_test.go
// alongside MergePluginKindsMap (K1-proper — the merge half of the loader moved to loaderkit).
