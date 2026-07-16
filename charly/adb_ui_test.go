package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// The dumpsys-window focus extraction (parseCurrentFocus) + the `adb current-focus` /
// `adb wait-ui-settled` UI-readiness methods left core in the adb → external-plugin
// dep-shed; their unit coverage lives in candy/plugin-adb. This file keeps the generic
// stdin-heredoc guard test, which is not adb-specific.

// TestWrapContainerCommand verifies the generic stdin-heredoc guard: an
// in-container command script must be wrapped in a brace group with stdin from
// /dev/null so a stdin-consuming subcommand (adb shell / ssh / read) cannot eat
// the rest of the heredoc-delivered script.
func TestWrapContainerCommand(t *testing.T) {
	script := "adb shell dumpsys window > /tmp/f\necho done"
	got := kit.WrapContainerCommand(script)
	want := "{ adb shell dumpsys window > /tmp/f\necho done\n} </dev/null"
	if got != want {
		t.Errorf("wrapContainerCommand()\n got=%q\nwant=%q", got, want)
	}
	if !strings.HasPrefix(got, "{ ") {
		t.Errorf("must open a brace group: %q", got)
	}
	if !strings.HasSuffix(got, "} </dev/null") {
		t.Errorf("must close the group with stdin from /dev/null: %q", got)
	}
	// The original script body must survive verbatim inside the group.
	if !strings.Contains(got, script) {
		t.Errorf("wrapped form dropped the original script body: %q", got)
	}
}
