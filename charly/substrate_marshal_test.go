package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opencharly/spec/spec"
)

// captureSubstrateProv stands in for a deploy-substrate provider: it records the env it is handed
// and echoes a minimal spec.DeployNode, so a test can assert WHAT the host marshalled onto the
// wire — the only way to prove the `Standalone.Body` arms ran.
type captureSubstrateProv struct {
	env spec.StructuralKindLoadEnv
}

func (p *captureSubstrateProv) Reserved() string     { return "pod" }
func (p *captureSubstrateProv) Class() ProviderClass { return ClassDeployTarget }

func (p *captureSubstrateProv) Invoke(_ context.Context, op *Operation) (*Result, error) {
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
