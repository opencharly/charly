package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// captureSubstrateProv stands in for a deploy-substrate provider: it records the env it is handed
// and echoes a minimal spec.DeployNode, so a test can assert WHAT the host marshalled onto the
// wire — the only way to prove the `Standalone.Body` arms ran.
type captureSubstrateProv struct {
	env    spec.StructuralKindLoadEnv
	rawEnv []string
}

func (p *captureSubstrateProv) Reserved() string { return "pod" }

// IsStructuralKind makes the double a STRUCTURAL carrier, which is the only kind for which the
// host builds the members env at all — the arm this test exists to exercise.
func (p *captureSubstrateProv) IsStructuralKind() bool { return true }
func (p *captureSubstrateProv) Class() ProviderClass   { return ClassDeployTarget }

func (p *captureSubstrateProv) Invoke(_ context.Context, op *Operation) (*Result, error) {
	p.rawEnv = append(p.rawEnv, string(op.Env))
	if err := json.Unmarshal(op.Env, &p.env); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(spec.DeployNode{Target: "pod", Image: "x"})
	return &Result{JSON: b}, nil
}

// TestFoldSubstrateKindMarshalsTheStandaloneBody pins the marshalling this PR adds: a deploy-shaped
// substrate node reaches the plugin as `Standalone{Shape:"deploy", Body:<json>}` — the body as
// BYTES, never as an authored Go struct on the wire. It fails without the change (a pre-cutover
// envelope carried the node itself) and, at run time, if the body stops riding the wire.
func TestFoldSubstrateKindMarshalsTheStandaloneBody(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	prov := &captureSubstrateProv{}
	pn := singleParsedNode(t, "mypod:\n  pod:\n    image: x\n")
	var acc spec.MaterializedProject
	if err := foldSubstrateKind(prov, pn, &acc); err != nil {
		t.Fatalf("fold substrate kind: %v", err)
	}
	if prov.env.Standalone == nil {
		t.Fatal("the host sent no Standalone envelope — the body did not ride the wire")
	}
	if prov.env.Standalone.Shape != "deploy" {
		t.Fatalf("shape = %q, want \"deploy\"", prov.env.Standalone.Shape)
	}
	if len(prov.env.Standalone.Body) == 0 {
		t.Fatal("the standalone body is empty — the node was not marshalled into it")
	}
	var round spec.DeployNode
	if err := json.Unmarshal(prov.env.Standalone.Body, &round); err != nil {
		t.Fatalf("the body is not a deploy node: %v", err)
	}
	if round.Image != "x" {
		t.Fatalf("the body lost the authored image: %q", round.Image)
	}
}

// captureCandyProv records the env a CANDY provider is handed, so a test can assert that a
// candy-image / candy-layer node rides the wire as BYTES in Standalone.Body rather than as an
// authored struct.
type captureCandyProv struct {
	env spec.StructuralKindLoadEnv
}

func (p *captureCandyProv) Reserved() string             { return "candy" }
func (p *captureCandyProv) Class() ProviderClass         { return ClassKind }
func (p *captureCandyProv) DeclaredKindInputDef() string { return "#CandyValue" }

func (p *captureCandyProv) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if err := json.Unmarshal(op.Env, &p.env); err != nil {
		return nil, err
	}
	if op.Op == ops.OpLoad {
		b, _ := json.Marshal(spec.CandyModel{Name: "mybox"})
		return &Result{JSON: b}, nil
	}
	return &Result{JSON: []byte("{}")}, nil
}

// TestFoldSubstrateKindCarriesMembersAsBytes pins the members half of the marshalling: a structural
// kind's authored peer members ride the SAME opaque body as bytes — inside the deploy body's
// `member` field — never as an authored Go struct on the wire. Measured on this head: the probe
// env is `{"standalone":{"shape":"deploy","body":{"target":"pod","image":"x","member":[{"name":"peer",
// "position":"deploy-level","node":{"target":"pod","image":"y"}}]}}}`.
func TestFoldSubstrateKindCarriesMembersAsBytes(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	prov := &captureSubstrateProv{}
	pn := singleParsedNode(t, "mypod:\n  pod:\n    image: x\n  peer:\n    pod:\n      image: y\n")
	var acc spec.MaterializedProject
	if err := foldSubstrateKind(prov, pn, &acc); err != nil {
		t.Fatalf("fold substrate kind: %v", err)
	}
	if prov.env.Standalone == nil || len(prov.env.Standalone.Body) == 0 {
		t.Fatal("no opaque body reached the plugin")
	}
	// The member must be INSIDE those bytes, and must survive a decode: that is what makes the
	// body the complete carrier rather than a scalar echo.
	var round struct {
		Target string `json:"target"`
		Member []struct {
			Name string `json:"name"`
			Node struct {
				Image string `json:"image"`
			} `json:"node"`
		} `json:"member"`
	}
	if err := json.Unmarshal(prov.env.Standalone.Body, &round); err != nil {
		t.Fatalf("the body is not a deploy node: %v", err)
	}
	if len(round.Member) != 1 || round.Member[0].Name != "peer" || round.Member[0].Node.Image != "y" {
		t.Fatalf("the authored member did not ride the body as bytes: %s", prov.env.Standalone.Body)
	}
}

// TestFoldCandyKindMarshalsTheStandaloneBody pins the candy marshalling arm: a `candy:` node
// (box⊻layer) reaches its plugin as `Standalone{Shape:"candy-image"|"candy-layer", Body:<json>}`.
// It fails if the arm stops marshalling the node into the opaque body.
func TestFoldCandyKindMarshalsTheStandaloneBody(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	prov := &captureCandyProv{}
	pn := singleParsedNode(t, "mybox:\n  candy:\n    base: fedora\n")
	var acc spec.MaterializedProject
	if err := foldCandyKind(prov, pn, &acc); err != nil {
		t.Fatalf("fold candy kind: %v", err)
	}
	if prov.env.Standalone == nil {
		t.Fatal("the host sent no Standalone envelope — the candy body did not ride the wire")
	}
	if prov.env.Standalone.Shape != "candy-image" && prov.env.Standalone.Shape != "candy-layer" {
		t.Fatalf("shape = %q, want candy-image or candy-layer", prov.env.Standalone.Shape)
	}
	if len(prov.env.Standalone.Body) == 0 {
		t.Fatal("the standalone body is empty — the candy node was not marshalled into it")
	}
}

// structProbeProv is a ClassKind structural carrier; registered, it is what makes the host build
// the members channel instead of a standalone body.
type structProbeProv struct {
	capMeta
	env  spec.StructuralKindLoadEnv
	seen bool
}

func (p *structProbeProv) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if err := json.Unmarshal(op.Env, &p.env); err != nil {
		return nil, err
	}
	p.seen = true
	b, _ := json.Marshal(spec.DeployNode{Target: "pod", Image: "x"})
	return &Result{JSON: b}, nil
}

// TestRunPluginKindMarshalsEveryMemberAsBytes pins the members channel: a structural kind's
// authored resource members reach the plugin as OPAQUE BYTES (`Env.Members[name]`), never as an
// authored Go struct. It fails without the arm and, at run time, if the wire stops carrying bytes.
func TestRunPluginKindMarshalsEveryMemberAsBytes(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if err := registerPluginUnitSchema("structprobe", PluginSchema{
		CueSource: "\n#StructprobeValue: {}\n",
		InputDefs: map[string]string{provKey(ClassKind, "structprobe"): "#StructprobeValue"},
	}); err != nil {
		t.Fatalf("register the structprobe unit: %v", err)
	}
	prov := &structProbeProv{capMeta: capMeta{class: ClassKind, word: "structprobe", structural: true, inputDef: "#StructprobeValue"}}
	RegisterBuiltinProvider(prov)

	pn := spec.ParsedNode{
		Name: "probe", Disc: "structprobe",
		Children: []*spec.ParsedNode{{Name: "peer", Disc: "pod", Body: spec.RawBody(`{"image":"y"}`)}},
	}
	var acc spec.MaterializedProject
	if err := runPluginKind(prov, pn, &acc); err != nil {
		t.Fatalf("run plugin kind: %v", err)
	}
	if !prov.seen {
		t.Fatal("the provider was never invoked")
	}
	if len(prov.env.Members) != 1 || len(prov.env.Members["peer"]) == 0 {
		t.Fatalf("the member did not ride the members channel as bytes: %v", prov.env.Members)
	}
}
