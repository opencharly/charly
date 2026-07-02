package main

import (
	"strings"
	"testing"

	"github.com/opencharly/charly/charly/plugin/sdk"
	"github.com/opencharly/charly/charly/spec"
)

// methods_test.go covers the PLUGIN-side helpers ported out-of-process from
// charly/record.go (the deleted host-side RecordCmd): the pure path/name builders and the
// required-modifier check that moved here from the host's former in-proc live-verb contract.
// The venue-driving methods (start/stop/list/cmd) need a live executor reverse channel and
// are exercised by the R10 bed (the sway-browser-vnc `record: start`), not these unit tests.

func TestRecordSessionName(t *testing.T) {
	cases := []struct{ name, want string }{
		{"default", "record-default"},
		{"demo", "record-demo"},
		{"my-recording", "record-my-recording"},
		{"test_123", "record-test_123"},
	}
	for _, tc := range cases {
		if got := recordSessionName(tc.name); got != tc.want {
			t.Errorf("recordSessionName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRecordingFilePath(t *testing.T) {
	cases := []struct{ name, mode, want string }{
		{"demo", "terminal", "/tmp/charly-recordings/demo.cast"},
		{"demo", "desktop", "/tmp/charly-recordings/demo.mp4"},
		{"walkthrough", "desktop", "/tmp/charly-recordings/walkthrough.mp4"},
		{"test-1", "terminal", "/tmp/charly-recordings/test-1.cast"},
	}
	for _, tc := range cases {
		if got := recordingFilePath(tc.name, tc.mode); got != tc.want {
			t.Errorf("recordingFilePath(%q, %q) = %q, want %q", tc.name, tc.mode, got, tc.want)
		}
	}
}

// TestRecordName covers the CLI `-n` default (empty record_name → "default").
func TestRecordName(t *testing.T) {
	if got := recordName(&spec.Op{}); got != "default" {
		t.Errorf("recordName(empty) = %q, want default", got)
	}
	if got := recordName(&spec.Op{RecordName: "demo"}); got != "demo" {
		t.Errorf("recordName(demo) = %q, want demo", got)
	}
}

// TestRecordFps covers the CLI Fps default (0/unset → 30).
func TestRecordFps(t *testing.T) {
	if got := recordFps(&spec.Op{}); got != 30 {
		t.Errorf("recordFps(unset) = %d, want 30", got)
	}
	if got := recordFps(&spec.Op{RecordFps: 60}); got != 60 {
		t.Errorf("recordFps(60) = %d, want 60", got)
	}
}

// TestCheckRequiredModifiers mirrors the in-tree recordMethods Required specs that moved
// here: `stop` needs an artifact, `cmd` needs the text line; list/start need nothing.
func TestCheckRequiredModifiers(t *testing.T) {
	cases := []struct {
		method  string
		op      spec.Op
		wantErr string // substring; "" means no error
	}{
		{"list", spec.Op{Record: "list"}, ""},
		{"start", spec.Op{Record: "start"}, ""},
		{"stop", spec.Op{Record: "stop"}, "artifact"},
		{"stop", spec.Op{Record: "stop", Artifact: "/tmp/x.cast"}, ""},
		{"cmd", spec.Op{Record: "cmd"}, "text"},
		{"cmd", spec.Op{Record: "cmd", Text: "echo hi"}, ""},
	}
	for _, tc := range cases {
		err := sdk.CheckRequiredModifiers(tc.method, &tc.op, requiredModifiers, modifierZero)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", tc.method, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: expected error containing %q, got %v", tc.method, tc.wantErr, err)
		}
	}
}
