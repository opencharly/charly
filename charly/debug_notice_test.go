package main

import (
	"strings"
	"testing"
)

// TestDebugNotice_CarriesRepoOverride: the failed-bed inspect hint must carry
// the active CHARLY_REPO_OVERRIDE so `charly check live` reproduces the bed's
// LOCAL-checkout state (a bare command would resolve @main-published plugins —
// a different deployment).
func TestDebugNotice_CarriesRepoOverride(t *testing.T) {
	t.Setenv("CHARLY_REPO_OVERRIDE", "github.com/opencharly/charly=/home/x/charly")
	var b strings.Builder
	printDebugRetentionNotice(&b, "check-foo", BundleNode{Target: "pod"})
	out := b.String()
	if !strings.Contains(out, "CHARLY_REPO_OVERRIDE='github.com/opencharly/charly=/home/x/charly' charly check live check-foo") {
		t.Fatalf("pod hint must carry the override:\n%s", out)
	}
	// Without an override, the bare command (no empty prefix).
	t.Setenv("CHARLY_REPO_OVERRIDE", "")
	b.Reset()
	printDebugRetentionNotice(&b, "check-bar", BundleNode{Target: "vm", From: "vm-tpl"})
	if out := b.String(); !strings.Contains(out, "inspect: charly check live check-bar |") || strings.Contains(out, "CHARLY_REPO_OVERRIDE=''") {
		t.Fatalf("no-override hint must be the bare command:\n%s", out)
	}
}
