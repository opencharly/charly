package spec

import (
	"context"
	"encoding/json"
	"testing"
)

// fakeParentExec is a minimal DeployExecutor stub — just enough to make EmitOpts.ParentExec a
// live, non-nil interface value for the two tests below. Every method is unreachable in this
// test; only its existence as an interface value (not its behavior) matters.
type fakeParentExec struct{}

func (fakeParentExec) Venue() string                                              { return "fake" }
func (fakeParentExec) RunSystem(context.Context, string, EmitOpts) error          { return nil }
func (fakeParentExec) RunUser(context.Context, string, EmitOpts) error            { return nil }
func (fakeParentExec) RunBuilder(context.Context, BuilderRunOpts) ([]byte, error) { return nil, nil }
func (fakeParentExec) PutFile(context.Context, string, string, uint32, bool, EmitOpts) error {
	return nil
}
func (fakeParentExec) GetFile(context.Context, string, bool, EmitOpts) ([]byte, error) {
	return nil, nil
}
func (fakeParentExec) RunCapture(context.Context, string) (string, string, int, error) {
	return "", "", 0, nil
}
func (fakeParentExec) RunInteractive(context.Context, string) (int, error) { return 0, nil }
func (fakeParentExec) RunStream(context.Context, string) (int, error)      { return 0, nil }
func (fakeParentExec) Kind() string                                        { return "fake" }
func (fakeParentExec) ResolveHome(context.Context, string) (string, error) { return "", nil }

// TestEmitOpts_RawMarshalRoundTripFailsWithParentExec proves the DEFECT an R10 bed
// (check-sidecar-pod) caught in this cutover: a raw EmitOpts whose ParentExec is a live,
// non-nil interface value (exactly what a nested-child deploy's composed NestedExecutor is)
// marshals fine but does NOT unmarshal back — the bare DeployExecutor interface field has no
// concrete type to decode into. This is why the deploy-dispatch envelope (S3b,
// charly/unified_targets.go's pluginDeployTarget.Add/Update) must NEVER marshal a raw EmitOpts
// across the wire; it must project through LifecycleOptsFromEmit first (see the sibling test
// below). Guards against this defect resurfacing silently in a future refactor.
func TestEmitOpts_RawMarshalRoundTripFailsWithParentExec(t *testing.T) {
	o := EmitOpts{DryRun: true, ParentExec: fakeParentExec{}}
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("marshal itself should succeed (ParentExec has no exported cyclic fields): %v", err)
	}
	var back EmitOpts
	if err := json.Unmarshal(b, &back); err == nil {
		t.Fatalf("want an unmarshal error decoding an object into the bare DeployExecutor interface field, got nil (back=%+v)", back)
	}
}

// TestLifecycleOptsFromEmit_SurvivesTheWireRegardlessOfParentExec proves the FIX: projecting
// EmitOpts through LifecycleOptsFromEmit before marshaling round-trips correctly even when
// ParentExec/ParentNode are live/non-nil — reproducing exactly the nested-child-deploy shape
// (charly/unified_targets.go's pluginDeployTarget.Add, dispatched to candy/plugin-fleet) that
// crashed with `json: cannot unmarshal object into Go struct field EmitOpts.ParentExec of type
// spec.DeployExecutor` before this fix.
func TestLifecycleOptsFromEmit_SurvivesTheWireRegardlessOfParentExec(t *testing.T) {
	o := EmitOpts{
		DryRun: true, AllowRepoChanges: true, AllowRootTasks: true, WithServices: true,
		AssumeYes: true, Verify: true, Pull: true, SkipIncompatible: true,
		BuilderImageOverride: "fedora.fedora-builder",
		ParentExec:           fakeParentExec{}, // the field that must NEVER cross the wire
		ParentNode:           &FleetNode{},
	}
	b, err := json.Marshal(LifecycleOptsFromEmit(o))
	if err != nil {
		t.Fatalf("marshal projected LifecycleOpts: %v", err)
	}
	var back LifecycleOpts
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal projected LifecycleOpts: %v", err)
	}
	want := LifecycleOpts{
		DryRun: true, AllowRepoChanges: true, AllowRootTasks: true, WithServices: true,
		AssumeYes: true, Verify: true, Pull: true, SkipIncompatible: true,
		BuilderImageOverride: "fedora.fedora-builder",
	}
	if back != want {
		t.Fatalf("want %+v, got %+v", want, back)
	}
}
