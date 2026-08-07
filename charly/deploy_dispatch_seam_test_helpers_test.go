package main

// deploy_dispatch_seam_test_helpers_test.go — drives sdk/deploykit's per-host deploy-state
// WRITERS (SaveDeployState, RemoveVmDeployEntry) through the REAL PRODUCTION seam
// (dispatchDeployTarget → the compiled-in command:fleet plugin's Invoke(OpDeployDispatch) →
// handleDeployApply/handleDeployDel → lifecycleInvoke → the plugin's own deployMarshalNode +
// loadFleetConfig + deploykit writer) instead of calling the sdk writer directly — the
// seam-drive conversion for the #55 final-tail bed-persist/deploy-state cluster (team-lead
// directive, 2026-08-03). A throwaway fake ClassDeployTarget lifecycle provider, registered
// per-test under a unique word, plays the substrate's PrepareVenue/Execute/PostApply (Add) or
// TeardownExecutor/PostTeardown (Del) legs — exactly the shape a real lifecycle substrate
// (candy/plugin-vm) implements. Both this file and its callers import zero sdk packages.
import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// seamLifecycleProvider is a throwaway ClassDeployTarget provider for driving an add THEN a del
// dispatch (the same registered instance serves both — Add/Del of the SAME name share one
// ledger-keyed lifecycle in production too) through the real handleDeployApply/handleDeployDel
// machinery. addState feeds the OpPrepareVenue reply's State patch (the thing under test in the
// SaveDeployState scenarios); delRemoveEntries feeds the OpPostTeardown reply's RemoveEntries
// (the thing under test in the RemoveVmDeployEntry scenarios) — mutated in place between the two
// dispatch calls, a pointer receiver so the SAME registered provider instance sees the update
// (the registry cannot re-register the same (class, word) key twice). ops.OpExecute/OpPostApply/
// OpTeardownExecutor are no-ops — the substrate's actual apply/teardown effect is irrelevant to
// what these tests assert.
type seamLifecycleProvider struct {
	word             string
	addState         json.RawMessage
	delRemoveEntries []string
}

func (p *seamLifecycleProvider) Reserved() string     { return p.word }
func (p *seamLifecycleProvider) Class() ProviderClass { return ClassDeployTarget }
func (p *seamLifecycleProvider) Invoke(_ context.Context, op *Operation) (*Result, error) {
	switch op.Op {
	case ops.OpPrepareVenue:
		b, err := json.Marshal(spec.PrepareVenueReply{State: p.addState})
		if err != nil {
			return nil, err
		}
		return &Result{JSON: b}, nil
	case ops.OpExecute, ops.OpPostApply:
		b, err := json.Marshal(spec.DeployReply{})
		if err != nil {
			return nil, err
		}
		return &Result{JSON: b}, nil
	case ops.OpTeardownExecutor:
		return &Result{JSON: []byte("{}")}, nil
	case ops.OpPostTeardown:
		b, err := json.Marshal(spec.PostTeardownReply{RemoveEntries: p.delRemoveEntries})
		if err != nil {
			return nil, err
		}
		return &Result{JSON: b}, nil
	}
	return nil, fmt.Errorf("seamLifecycleProvider: unsupported op %q", op.Op)
}

// testSeamProvider registers a fresh *seamLifecycleProvider under a per-test-unique word
// (t.Name() is unique within a process run) and restores the registry via
// t.Cleanup(snapshotProviderState()). Callers then drive testDispatchLifecycleAdd/Del against
// the SAME word — one registration serves both legs (Add/Del of the same deploy name share one
// ledger-keyed lifecycle in production too, and the registry rejects a duplicate (class, word)).
func testSeamProvider(t *testing.T) *seamLifecycleProvider {
	t.Helper()
	t.Cleanup(snapshotProviderState())
	p := &seamLifecycleProvider{word: "test-seam-" + t.Name()}
	if err := providerRegistry.register(p, "test-seam"); err != nil {
		t.Fatalf("register fake lifecycle provider: %v", err)
	}
	return p
}

// testDispatchLifecycleAddTolerant sets p's OpPrepareVenue reply state and drives it through the
// REAL dispatchDeployTarget("add"), returning the dispatch error to the caller instead of
// t.Fatal-ing — some scenarios (e.g. the abort-on-invalid-existing-file case) expect the
// production write path to REFUSE, surfacing as a dispatch error; the point under test is the
// on-disk byte-identity, not whether the dispatch call itself reports success.
func testDispatchLifecycleAddTolerant(t *testing.T, p *seamLifecycleProvider, name string, state spec.SaveDeployStateInput) error {
	t.Helper()
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal SaveDeployStateInput: %v", err)
	}
	p.addState = stateJSON
	req := spec.DeployTargetDispatchRequest{Op: "add", Name: name, Word: p.word, HasLifecycle: true}
	_, dispatchErr := dispatchDeployTarget(context.Background(), req, nil, buildEngineContext{}, false)
	return dispatchErr
}

// testDispatchLifecycleAdd is testDispatchLifecycleAddTolerant, but fails the test immediately on
// a dispatch error — the normal case for a scenario expecting the write to succeed cleanly.
func testDispatchLifecycleAdd(t *testing.T, p *seamLifecycleProvider, name string, state spec.SaveDeployStateInput) {
	t.Helper()
	if err := testDispatchLifecycleAddTolerant(t, p, name, state); err != nil {
		t.Fatalf("dispatchDeployTarget(add %q): %v", name, err)
	}
}
