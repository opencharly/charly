package bundle

import (
	"errors"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// deploy_target_bracket_test.go — coverage for runLifecycleBracket (FLOOR-SLIM-proper Unit-8):
// the Q1 resource-arbiter bracket's decision logic, relocated from the deleted
// charly/arbiter_bracket_test.go's arbiterBracketedStart/Stop coverage. Ported call-order
// assertions (acquire-before-dispatch, release-on-failure, release-after-stop) onto the pure
// helper via recording acquire/release/dispatch closures — no exec, no wire round-trip needed to
// prove the ORDER, which is the property that actually matters (the wire plumbing itself is
// exercised live by the R10 bed roster, per R7 — a unit test is not a substitute for that).

func bracketRecorder() (calls *[]string, acquire func() error, release func(), acquireErr *error) {
	var log []string
	var aErr error
	return &log, func() error {
			log = append(log, "acquire")
			return aErr
		}, func() {
			log = append(log, "release")
		}, &aErr
}

func TestRunLifecycleBracket_Start_Success_AcquiresNoRelease(t *testing.T) {
	log, acquire, release, _ := bracketRecorder()
	dispatchCalled := false
	err := runLifecycleBracket(sdk.OpStart, true, &spec.Deploy{}, acquire, release, func() error {
		*log = append(*log, "dispatch")
		dispatchCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dispatchCalled {
		t.Fatal("dispatch was not called")
	}
	want := []string{"acquire", "dispatch"}
	if !equalStrs(*log, want) {
		t.Fatalf("call order = %v, want %v", *log, want)
	}
}

func TestRunLifecycleBracket_Start_DispatchFails_ReleasesOnFailure(t *testing.T) {
	log, acquire, release, _ := bracketRecorder()
	dispatchErr := errors.New("start failed")
	err := runLifecycleBracket(sdk.OpStart, true, &spec.Deploy{}, acquire, release, func() error {
		*log = append(*log, "dispatch")
		return dispatchErr
	})
	if !errors.Is(err, dispatchErr) {
		t.Fatalf("error = %v, want %v", err, dispatchErr)
	}
	want := []string{"acquire", "dispatch", "release"}
	if !equalStrs(*log, want) {
		t.Fatalf("call order = %v, want %v", *log, want)
	}
}

func TestRunLifecycleBracket_Start_AcquireFails_DispatchNeverRuns(t *testing.T) {
	log, acquire, release, acquireErr := bracketRecorder()
	*acquireErr = errors.New("no capacity")
	dispatchCalled := false
	err := runLifecycleBracket(sdk.OpStart, true, &spec.Deploy{}, acquire, release, func() error {
		dispatchCalled = true
		return nil
	})
	if !errors.Is(err, *acquireErr) {
		t.Fatalf("error = %v, want the acquire error", err)
	}
	if dispatchCalled {
		t.Fatal("dispatch must not run when acquire fails")
	}
	want := []string{"acquire"}
	if !equalStrs(*log, want) {
		t.Fatalf("call order = %v, want %v (no release — nothing was ever acquired)", *log, want)
	}
}

func TestRunLifecycleBracket_Start_NoPlan_NeverBrackets(t *testing.T) {
	log, acquire, release, _ := bracketRecorder()
	err := runLifecycleBracket(sdk.OpStart, false /* hasPlan */, &spec.Deploy{}, acquire, release, func() error {
		*log = append(*log, "dispatch")
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"dispatch"}
	if !equalStrs(*log, want) {
		t.Fatalf("call order = %v, want %v (hasPlan=false must never bracket)", *log, want)
	}
}

func TestRunLifecycleBracket_Start_NilNode_NeverBrackets(t *testing.T) {
	// A nil node with hasPlan=true is a caller bug — treated as "no claim" rather than a panic
	// (the same defensive check the deleted charly/arbiter_bracket.go made).
	log, acquire, release, _ := bracketRecorder()
	err := runLifecycleBracket(sdk.OpStart, true, nil, acquire, release, func() error {
		*log = append(*log, "dispatch")
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"dispatch"}
	if !equalStrs(*log, want) {
		t.Fatalf("call order = %v, want %v (nil node must never bracket)", *log, want)
	}
}

func TestRunLifecycleBracket_Stop_Success_ReleasesAfter(t *testing.T) {
	log, acquire, release, _ := bracketRecorder()
	err := runLifecycleBracket(sdk.OpStop, true, &spec.Deploy{}, acquire, release, func() error {
		*log = append(*log, "dispatch")
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"dispatch", "release"}
	if !equalStrs(*log, want) {
		t.Fatalf("call order = %v, want %v", *log, want)
	}
}

func TestRunLifecycleBracket_Stop_DispatchFails_StillReleasesUnconditionally(t *testing.T) {
	log, acquire, release, _ := bracketRecorder()
	dispatchErr := errors.New("stop failed")
	err := runLifecycleBracket(sdk.OpStop, true, &spec.Deploy{}, acquire, release, func() error {
		*log = append(*log, "dispatch")
		return dispatchErr
	})
	if !errors.Is(err, dispatchErr) {
		t.Fatalf("error = %v, want %v", err, dispatchErr)
	}
	want := []string{"dispatch", "release"}
	if !equalStrs(*log, want) {
		t.Fatalf("call order = %v, want %v (a Stop-path error must still release, never leak the lease)", *log, want)
	}
}

func TestRunLifecycleBracket_Stop_NoPlan_NeverBrackets(t *testing.T) {
	log, acquire, release, _ := bracketRecorder()
	err := runLifecycleBracket(sdk.OpStop, false, &spec.Deploy{}, acquire, release, func() error {
		*log = append(*log, "dispatch")
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"dispatch"}
	if !equalStrs(*log, want) {
		t.Fatalf("call order = %v, want %v (hasPlan=false must never bracket)", *log, want)
	}
}

func TestRunLifecycleBracket_Logs_NeverBrackets(t *testing.T) {
	// Logs shares handleLifecycleSimple's dispatch path but is never a bracketed op — hasPlan is
	// always false for it in production (only Start/Stop populate lifecycleStartPlanHooks/
	// lifecycleStopPlanHooks), but this asserts the op-kind guard itself, independent of that.
	log, acquire, release, _ := bracketRecorder()
	err := runLifecycleBracket(sdk.OpLogs, true, &spec.Deploy{}, acquire, release, func() error {
		*log = append(*log, "dispatch")
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"dispatch"}
	if !equalStrs(*log, want) {
		t.Fatalf("call order = %v, want %v (op=logs must never bracket, even with hasPlan=true)", *log, want)
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
