package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// withFakeBracketShims swaps acquireForBracket/releaseForBracket for recorders appending into a
// SINGLE shared log (returned so the caller's dispatch closure appends to the SAME slice — true
// cross-call ordering, not just per-call-kind counts) and returns a restore func. Isolates the
// ordering assertions below from any live verb:arbiter plugin.
func withFakeBracketShims(t *testing.T, acquireErr error) (log *[]string, restore func()) {
	t.Helper()
	var l []string
	origAcquire, origRelease := acquireForBracket, releaseForBracket
	acquireForBracket = func(claimant string, node spec.BundleNode, transient bool) (*Lease, error) {
		l = append(l, "acquire")
		if acquireErr != nil {
			return nil, acquireErr
		}
		return &Lease{claimant: claimant, active: true}, nil
	}
	releaseForBracket = func(claimant string) { l = append(l, "release") }
	return &l, func() { acquireForBracket, releaseForBracket = origAcquire, origRelease }
}

func assertLog(t *testing.T, log *[]string, want ...string) {
	t.Helper()
	got := *log
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
}

func TestArbiterBracketedStart_OrderAcquireThenDispatch(t *testing.T) {
	log, restore := withFakeBracketShims(t, nil)
	defer restore()
	node := &spec.BundleNode{}
	err := arbiterBracketedStart("bed", node, true, func() error {
		*log = append(*log, "dispatch") // appends into the SAME shared log as acquire/release
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// FAILS ON REORDER: acquire must precede dispatch; success path releases nothing.
	assertLog(t, log, "acquire", "dispatch")
}

func TestArbiterBracketedStart_ReleaseOnDispatchFailure(t *testing.T) {
	log, restore := withFakeBracketShims(t, nil)
	defer restore()
	node := &spec.BundleNode{}
	wantErr := errors.New("boom")
	err := arbiterBracketedStart("bed", node, true, func() error {
		*log = append(*log, "dispatch")
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("want %v, got %v", wantErr, err)
	}
	// FAILS ON REORDER / MISSING RELEASE: acquire, dispatch, THEN release, on a failed dispatch.
	assertLog(t, log, "acquire", "dispatch", "release")
}

func TestArbiterBracketedStart_NoBracketWhenNoPlan(t *testing.T) {
	log, restore := withFakeBracketShims(t, nil)
	defer restore()
	err := arbiterBracketedStart("bed", &spec.BundleNode{}, false, func() error {
		*log = append(*log, "dispatch")
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// FAILS if the bracket wrongly acquires/releases when hasPlan=false (e.g. vm — manages its own claim).
	assertLog(t, log, "dispatch")
}

func TestArbiterBracketedStart_AcquireFailureNeverDispatches(t *testing.T) {
	log, restore := withFakeBracketShims(t, errors.New("no lease"))
	defer restore()
	err := arbiterBracketedStart("bed", &spec.BundleNode{}, true, func() error {
		*log = append(*log, "dispatch")
		return nil
	})
	if err == nil {
		t.Fatal("want error on acquire failure")
	}
	// FAILS if dispatch ran despite a failed acquire.
	assertLog(t, log, "acquire")
}

func TestArbiterBracketedStop_DispatchThenRelease(t *testing.T) {
	log, restore := withFakeBracketShims(t, nil)
	defer restore()
	err := arbiterBracketedStop("bed", true, func() error {
		*log = append(*log, "dispatch")
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// FAILS ON REORDER: the stop dispatch must run BEFORE the release.
	assertLog(t, log, "dispatch", "release")
}

func TestArbiterBracketedStop_ReleasesEvenOnDispatchError(t *testing.T) {
	log, restore := withFakeBracketShims(t, nil)
	defer restore()
	wantErr := errors.New("stop failed")
	err := arbiterBracketedStop("bed", true, func() error {
		*log = append(*log, "dispatch")
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("want %v, got %v", wantErr, err)
	}
	// FAILS if release is skipped on a failed stop dispatch (would leak the lease).
	assertLog(t, log, "dispatch", "release")
}

func TestArbiterBracketedStop_NoReleaseWhenNoPlan(t *testing.T) {
	log, restore := withFakeBracketShims(t, nil)
	defer restore()
	err := arbiterBracketedStop("bed", false, func() error {
		*log = append(*log, "dispatch")
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// FAILS if release runs when hasPlan=false (e.g. vm — manages its own claim).
	assertLog(t, log, "dispatch")
}

// TestPluginDeployTargetStop_PlanHookFailureNeverReleases proves the "not-attempted" path
// team-lead's parity review required (the former core-resident substrate lifecycle proxy's Stop,
// pre-S3b: release was skipped ONLY on a pre-dispatch plan-hook failure — the substrate was NEVER
// ASKED to stop, so the resource is presumably still running and the claim must survive; a wrong
// "always release after Stop" would free the claim for a still-running resource, letting a second
// claimant collide, e.g. a GPU token freed while the holder VM never actually stopped).
//
// pluginDeployTarget.Stop (unified_targets.go) reproduces this WITHOUT a wire "attempted" flag:
// it resolves the plan hook HOST-SIDE, synchronously, BEFORE ever calling arbiterBracketedStop —
// a plan-hook failure returns immediately, so the bracket (and therefore acquireForBracket/
// releaseForBracket) is never invoked at all. This test proves that composition directly: a
// registered Stop plan hook returning an error must produce ZERO acquire/release calls.
func TestPluginDeployTargetStop_PlanHookFailureNeverReleases(t *testing.T) {
	log, restore := withFakeBracketShims(t, nil)
	defer restore()

	const word = "test-stop-not-attempted"
	origHook, hadHook := lifecycleStopPlanHooks[word]
	wantErr := errors.New("plan resolve failed")
	lifecycleStopPlanHooks[word] = func(_ context.Context, _, _ string) (json.RawMessage, error) {
		return nil, wantErr
	}
	t.Cleanup(func() {
		if hadHook {
			lifecycleStopPlanHooks[word] = origHook
		} else {
			delete(lifecycleStopPlanHooks, word)
		}
	})

	tgt := &pluginDeployTarget{name: "test-deploy", word: word, hasLifecycle: true}
	err := tgt.Stop(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("want %v, got %v", wantErr, err)
	}
	// FAILS if the bracket's acquire/release fire despite the substrate never being asked to
	// stop — the not-attempted path a held claim must survive.
	assertLog(t, log)
}
