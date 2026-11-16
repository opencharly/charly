package main

// audit_event_test.go — the R7 half of P06 leg 3a: a test that FAILS when the mutation is
// not wired and passes when it is.
//
// It drives the REAL seam (dispatchAndRunLifecycle, the shared core every start/stop/
// update case calls), not a copy of the recording, and it reads the record back from the
// state root — so "exactly one record per act" (D-AUDIT-1) is asserted against the
// artifact an auditor would read, not against a counter in the test.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/spec/spec"
)

// isolateStateRoot points the ONE roots resolver's state root at a temp dir, so the test
// reads exactly the records this test wrote (storeroot.RootState honours XDG_STATE_HOME).
func isolateStateRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	return filepath.Join(dir, "charly", auditRecordFile)
}

// readAuditRecords parses the append-only file back into records. A missing file is zero
// records, not an error: that IS the "nothing was recorded" state the test must see.
func readAuditRecords(t *testing.T, path string) []AuditEvent {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read audit file %s: %v", path, err)
	}
	var out []AuditEvent
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var ev AuditEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("audit line is not a record: %v\nline: %s", err, line)
		}
		out = append(out, ev)
	}
	return out
}

// TestDispatchAndRunLifecycle_AppendsExactlyOneRecord is the wiring proof: one act, one
// record. The act is `start` on a node the dispatch cannot resolve, so it FAILS — and a
// failed act is still an act (D-AUDIT-1 counts mutations, not successes).
func TestDispatchAndRunLifecycle_AppendsExactlyOneRecord(t *testing.T) {
	path := isolateStateRoot(t)

	// A nil node is the one input dispatchLifecycleTarget rejects before any provider is
	// touched: the act is attempted, and fails at dispatch.
	err := dispatchAndRunLifecycle("start", nil, "check-audit", "pod-a", func(spec.LifecycleTarget) error {
		t.Fatal("the op body must not run when the target cannot be resolved")
		return nil
	})
	if err == nil {
		t.Fatal("want a dispatch error for a nil node, got nil")
	}

	recs := readAuditRecords(t, path)
	if len(recs) != 1 {
		t.Fatalf("want exactly 1 audit record for one act, got %d (records: %+v)", len(recs), recs)
	}
	got := recs[0]
	if got.Event != auditEventActivate {
		t.Errorf("event = %q, want %q (start brings a deployment to life)", got.Event, auditEventActivate)
	}
	if got.Outcome != auditOutcomeFailed {
		t.Errorf("outcome = %q, want %q", got.Outcome, auditOutcomeFailed)
	}
	if got.Action != "start" {
		t.Errorf("action = %q, want %q", got.Action, "start")
	}
	if got.Target != "check-audit.pod-a" {
		t.Errorf("target = %q, want %q", got.Target, "check-audit.pod-a")
	}
	if got.Identity == "" {
		t.Error("identity is empty; D-AUDIT-2 requires who acted")
	}
	if _, perr := time.Parse(time.RFC3339, got.At); perr != nil {
		t.Errorf("at = %q is not an RFC3339 #Timestamp: %v", got.At, perr)
	}
}

// TestReadOnlyLifecycleOpAppendsNoRecord is the other half of "exactly one per MUTATION":
// a read-only op must record nothing, or the file becomes a log of observations.
func TestReadOnlyLifecycleOpAppendsNoRecord(t *testing.T) {
	path := isolateStateRoot(t)
	if err := dispatchAndRunLifecycle("logs", nil, "check-audit", "pod-a", func(spec.LifecycleTarget) error {
		return nil
	}); err == nil {
		t.Fatal("want a dispatch error for a nil node, got nil")
	}
	if recs := readAuditRecords(t, path); len(recs) != 0 {
		t.Fatalf("a read-only op recorded %d record(s): %+v", len(recs), recs)
	}
}

// TestRunLifecycleAct_Outcome proves the success branch through the same recorder the
// dispatch calls: ok on success, failed on failure, one record either way, and the act's
// own error returned unchanged (the record never masks it).
func TestRunLifecycleAct_Outcome(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		path := isolateStateRoot(t)
		if err := runLifecycleAct("stop", "check-audit", "pod-a", nil, func(spec.LifecycleTarget) error {
			return nil
		}); err != nil {
			t.Fatalf("act returned %v, want nil", err)
		}
		recs := readAuditRecords(t, path)
		if len(recs) != 1 {
			t.Fatalf("want 1 record, got %d", len(recs))
		}
		if recs[0].Outcome != auditOutcomeOK {
			t.Errorf("outcome = %q, want %q", recs[0].Outcome, auditOutcomeOK)
		}
		if recs[0].Event != auditEventMutate || recs[0].Action != "stop" {
			t.Errorf("event/action = %q/%q, want %q/%q", recs[0].Event, recs[0].Action, auditEventMutate, "stop")
		}
	})

	t.Run("failed", func(t *testing.T) {
		path := isolateStateRoot(t)
		boom := errors.New("boom")
		err := runLifecycleAct("update", "check-audit", "pod-a", nil, func(spec.LifecycleTarget) error {
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("act error = %v, want the act's own %v", err, boom)
		}
		recs := readAuditRecords(t, path)
		if len(recs) != 1 {
			t.Fatalf("want 1 record, got %d", len(recs))
		}
		if recs[0].Outcome != auditOutcomeFailed {
			t.Errorf("outcome = %q, want %q", recs[0].Outcome, auditOutcomeFailed)
		}
	})
}

// TestAuditRef_NormalizesToContractPattern keeps the record schema-valid: #Ref
// (DESIGN.cue:51) admits only lowercase hyphen/dot-separated segments.
func TestAuditRef_NormalizesToContractPattern(t *testing.T) {
	for in, want := range map[string]string{
		"check-audit.pod-a": "check-audit.pod-a",
		"Check Audit POD":   "check-audit-pod",
		"9leading":          "leading",
		"":                  "unknown",
	} {
		if got := auditRef(in); got != want {
			t.Errorf("auditRef(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPodLifecycleUpdate_AppendsExactlyOneRecord drives the `update` op through the host
// builder: it is the ONE mutating op that does not delegate to dispatchAndRunLifecycle (its
// body is dispatchByDeployTarget's own per-target dispatch), so its own call site needs its
// own proof.
func TestPodLifecycleUpdate_AppendsExactlyOneRecord(t *testing.T) {
	path := isolateStateRoot(t)
	payload, err := json.Marshal(spec.PodUpdatePayload{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	_, herr := hostBuildPodLifecycle(context.Background(), spec.PodLifecycleRequest{
		Op: "update", Box: "check-audit", Instance: "pod-a", Payload: payload,
	}, buildEngineContext{})
	if herr == nil {
		t.Fatal("want an error for a nil deploy node, got nil")
	}
	recs := readAuditRecords(t, path)
	if len(recs) != 1 {
		t.Fatalf("want exactly 1 audit record for the update act, got %d (records: %+v)", len(recs), recs)
	}
	if recs[0].Event != auditEventMutate || recs[0].Action != "update" || recs[0].Outcome != auditOutcomeFailed {
		t.Errorf("event/action/outcome = %q/%q/%q, want %q/%q/%q",
			recs[0].Event, recs[0].Action, recs[0].Outcome,
			auditEventMutate, "update", auditOutcomeFailed)
	}
}
