package main

// check_verb_reply_test.go — the OUT-OF-PROCESS verb reply decode (opencharly/charly#780, consumer
// half).
//
// checkResultFromVerbReply is the ONE place the host turns a verb provider's wire reply into the
// CheckResult the plan carries and the ledger records. It decodes through the contract module's
// own decoder, so the producer's CapturedValue — sent by the SDK's ServeCheckVerb on the
// out-of-process family (cdp/kube/appium/adb/wl/…) — reaches the ledger instead of being dropped
// by a locally re-declared {status,message} shape.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// TestCheckResultFromVerbReplyCarriesTheCapturedValue is the guard for the consumer half: a
// reply carrying a structured capture lands as a JSON document on the CheckResult.
func TestCheckResultFromVerbReplyCarriesTheCapturedValue(t *testing.T) {
	reply, err := ops.ResultJSONCaptured("pass", "wl: screencap wrote 67 bytes to /tmp/screencap.png", map[string]any{
		"bytes": 67,
		"path":  "/tmp/screencap.png",
	})
	if err != nil {
		t.Fatalf("wire builder: %v", err)
	}

	got := checkResultFromVerbReply("wl", reply.GetResultJson())
	if got.Status != spec.StatusPass || got.Message != "wl: screencap wrote 67 bytes to /tmp/screencap.png" {
		t.Fatalf("verdict = (%v, %q), want (pass, the producer's message)", got.Status, got.Message)
	}
	if got.CapturedValue == "" {
		t.Fatalf("the capture was DROPPED by the host decode: the wire carried %s", reply.GetResultJson())
	}
	var probe struct {
		Bytes int    `json:"bytes"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal([]byte(got.CapturedValue), &probe); err != nil {
		t.Fatalf("the capture did not land as a JSON document: %v (%s)", err, got.CapturedValue)
	}
	if probe.Bytes != 67 || probe.Path != "/tmp/screencap.png" {
		t.Fatalf("the capture landed corrupt: %+v", probe)
	}
}

// TestCheckResultFromVerbReplyWithoutCaptureIsUnchanged pins the compatibility half: a reply with
// no capture keeps the byte-for-byte behaviour of the {status,message} decode it replaces, for
// all three statuses.
func TestCheckResultFromVerbReplyWithoutCaptureIsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		wire string
		want spec.Status
	}{
		{"pass", spec.StatusPass},
		{"fail", spec.StatusFail},
		{"skip", spec.StatusSkip},
	} {
		reply, err := ops.ResultJSON(tc.wire, "message for "+tc.wire)
		if err != nil {
			t.Fatalf("wire builder: %v", err)
		}
		got := checkResultFromVerbReply("probe", reply.GetResultJson())
		if got.Status != tc.want || got.Message != "message for "+tc.wire {
			t.Fatalf("wire %q -> (%v, %q), want (%v, %q)", tc.wire, got.Status, got.Message, tc.want, "message for "+tc.wire)
		}
		if got.CapturedValue != "" {
			t.Fatalf("a captureless reply carried a capture: %q", got.CapturedValue)
		}
	}
}

// TestCheckResultFromVerbReplyMalformedIsAVerbNamingFail proves a corrupt reply can never be
// mistaken for a verdict: it fails, naming the verb, with no capture (R1).
func TestCheckResultFromVerbReplyMalformedIsAVerbNamingFail(t *testing.T) {
	got := checkResultFromVerbReply("kube", json.RawMessage("not json"))
	if got.Status != spec.StatusFail {
		t.Fatalf("a malformed reply produced status %v, want fail", got.Status)
	}
	if !strings.Contains(got.Message, `verb "kube"`) {
		t.Fatalf("the failure does not name the verb: %q", got.Message)
	}
	if got.CapturedValue != "" {
		t.Fatalf("a malformed reply carried a capture: %q", got.CapturedValue)
	}
}

// TestCheckResultFromVerbReplyUnknownStatusIsAFail pins the default arm the hand-rolled switch
// had: an unrecognised status is a FAIL, never a silent pass.
func TestCheckResultFromVerbReplyUnknownStatusIsAFail(t *testing.T) {
	got := checkResultFromVerbReply("wl", json.RawMessage(`{"status":"maybe","message":"who knows"}`))
	if got.Status != spec.StatusFail || got.Message != "who knows" {
		t.Fatalf("unknown status -> (%v, %q), want (fail, the message)", got.Status, got.Message)
	}
}
