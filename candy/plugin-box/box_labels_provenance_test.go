package box

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/hostenv"
	"github.com/opencharly/spec/spec"
)

// box_labels_provenance_test.go — `charly box labels` is the charly-native R8 artifact check, and
// this cutover reclassifies it as a VERDICT verb resolving through the staleness guard. A verdict
// verb must name the artifact it judged: without an `Image:` line a reader gets a capability
// contract with no way to audit WHICH image produced it. That is sharpest here because four
// unpinned `box labels` calls in box/arch's check-agent-pod bed are this cutover's headline
// evidence that the guard engages — and none of them could say what it read.
//
// The line goes to STDERR so `--format <key>`'s single raw stdout value — the scripting contract
// every plan step pipes into grep — is byte-unchanged; both are asserted below.
func captureStdioForLabels(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	oo, oe := os.Stdout, os.Stderr
	ro, wo, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	re, we, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = wo, we
	dout, derr := make(chan string, 1), make(chan string, 1)
	go func() { var b bytes.Buffer; _, _ = b.ReadFrom(ro); dout <- b.String() }()
	go func() { var b bytes.Buffer; _, _ = b.ReadFrom(re); derr <- b.String() }()
	fn()
	_ = wo.Close()
	_ = we.Close()
	os.Stdout, os.Stderr = oo, oe
	return <-dout, <-derr
}

const provRef = "ghcr.io/opencharly/check-agent-box:check-agent-pod-2026.227.1341"

func stubLabelsStorage(t *testing.T) {
	t.Helper()
	// Stub kit.InspectImageLabels, NOT container.InspectLabels. container.InspectImageLabels is a
	// plain FUNCTION that shells out to `podman inspect`, and kit.InspectImageLabels copied that
	// function value at init — so overriding container.InspectLabels (a var pointing at the same
	// function) does NOT redirect what dispatchLabels calls. The first version of this test did
	// exactly that and PASSED anyway, against the real image store; it only surfaced when a
	// concurrent bed's retention sweep deleted the image it had been silently depending on. A test
	// that reads real storage is not a test of this code.
	origRT, origList, origInspect, origExists := kit.ResolveRuntime, container.ListLocalImages, kit.InspectImageLabels, kit.LocalImageExists
	t.Cleanup(func() {
		kit.ResolveRuntime, container.ListLocalImages, kit.InspectImageLabels, kit.LocalImageExists = origRT, origList, origInspect, origExists
	})
	kit.LocalImageExists = func(string, string) bool {
		t.Fatal("dispatchLabels fell through to a REAL local-storage probe — the stubs are not covering the path under test")
		return false
	}
	kit.ResolveRuntime = func() (*hostenv.ResolvedRuntime, error) {
		return &hostenv.ResolvedRuntime{RunEngine: "podman"}, nil
	}
	container.ListLocalImages = func(string) ([]container.LocalImageInfo, error) {
		return []container.LocalImageInfo{{
			ID: "aaa", Created: 1786200000, Names: []string{provRef},
			Labels: map[string]string{spec.LabelBox: "check-agent-box", spec.LabelVersion: "2026.226.1600"},
		}}, nil
	}
	kit.InspectImageLabels = func(string, string) (map[string]string, error) {
		return map[string]string{
			spec.LabelBox:     "check-agent-box",
			spec.LabelVersion: "2026.226.1600",
		}, nil
	}
}

// TestBoxLabelsPrintsProvenance — the default (whole-contract) form names the artifact on stderr
// and leaves the label payload on stdout. Fails without the fix: pre-fix stderr is empty.
func TestBoxLabelsPrintsProvenance(t *testing.T) {
	stubLabelsStorage(t)
	stdout, stderr := captureStdioForLabels(t, func() {
		if err := dispatchLabels([]string{"check-agent-box"}); err != nil {
			t.Fatalf("dispatchLabels: %v", err)
		}
	})
	if !strings.Contains(stderr, "Image: "+provRef) {
		t.Fatalf("no provenance on stderr:\n%q\nwant an `Image: %s` line — a capability report a reader cannot trace to an artifact is unauditable", stderr, provRef)
	}
	if !strings.Contains(stdout, "ai.opencharly.box=check-agent-box") {
		t.Fatalf("label payload missing from stdout:\n%q", stdout)
	}
}

// TestBoxLabelsFormatStdoutIsUnchanged pins the contract the stderr choice protects: `--format
// <key>` must still emit EXACTLY the one raw value on stdout, because every plan step that uses
// this verb pipes stdout into grep. A provenance line on stdout would have broken those beds.
func TestBoxLabelsFormatStdoutIsUnchanged(t *testing.T) {
	stubLabelsStorage(t)
	stdout, stderr := captureStdioForLabels(t, func() {
		if err := dispatchLabels([]string{"check-agent-box", "--format", "box"}); err != nil {
			t.Fatalf("dispatchLabels --format: %v", err)
		}
	})
	if stdout != "check-agent-box\n" {
		t.Fatalf("--format stdout = %q, want exactly %q — the scripting contract must be byte-unchanged", stdout, "check-agent-box\n")
	}
	if !strings.Contains(stderr, "Image: "+provRef) {
		t.Fatalf("--format path printed no provenance on stderr:\n%q", stderr)
	}
}
