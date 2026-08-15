package check

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// check_cmd_provenance_test.go — a verdict verb must ALWAYS name the artifact it judged.
//
// `charly check box` bailed on the no-plan path BEFORE printing its `Image:` line, so an image
// carrying no baked plan produced no ref, no version, nothing — and exited 0. That mattered
// directly: the mitigation this cutover told operators to use while the resolver was being fixed
// was "read the Image: line before believing a verdict", and on exactly those images there was no
// line to read. Reported twice independently, from two different cutovers.
//
// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return <-done
}

// TestCheckBoxNoStepsPrintsProvenance is the gate: the no-plan path must still name the ref.
// Fails without the fix — the pre-fix arm printed only the prose.
func TestCheckBoxNoStepsPrintsProvenance(t *testing.T) {
	const ref = "ghcr.io/opencharly/fedora-nonfree:2026.227.0836"
	orig := hostCheckRun
	defer func() { hostCheckRun = orig }()
	hostCheckRun = func(spec.CheckRunRequest) (kit.CheckRunReply, error) {
		return kit.CheckRunReply{Image: ref, NoSteps: true}, nil
	}

	out := captureStderr(t, func() {
		cmd := &CheckBoxCmd{Image: "fedora-nonfree"}
		if err := cmd.Run(); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	if !strings.Contains(out, "Image: "+ref) {
		t.Fatalf("no-plan path printed no provenance:\n%s\nwant an `Image: %s` line — a verdict with no artifact identity is unverifiable, and this is the path where it matters most", out, ref)
	}
	if !strings.Contains(out, "No plan steps defined") {
		t.Fatalf("the no-plan message was lost:\n%s", out)
	}
}

// TestFeatureBoxNoStepsPrintsProvenance gates the SIBLING arm. The rule is cited as R3 in this
// cutover's own narrative — "both build-scope verbs print it FIRST" — and an ungated half is not
// applied: deleting the line from feature_box_gather.go broke no test at all, which is how a
// principle quietly becomes true of one caller and false of the other.
func TestFeatureBoxNoStepsPrintsProvenance(t *testing.T) {
	const ref = "ghcr.io/opencharly/fedora-nonfree:2026.227.0836"
	orig := hostCheckRun
	defer func() { hostCheckRun = orig }()
	hostCheckRun = func(spec.CheckRunRequest) (kit.CheckRunReply, error) {
		return kit.CheckRunReply{Image: ref, NoSteps: true}, nil
	}

	out := captureStderr(t, func() {
		cmd := &CheckFeatureBoxCmd{Image: "fedora-nonfree"}
		if err := cmd.Run(); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	if !strings.Contains(out, "Image: "+ref) {
		t.Fatalf("feature-box no-plan path printed no provenance:\n%s\nwant an `Image: %s` line", out, ref)
	}
	if !strings.Contains(out, "No plan steps baked") {
		t.Fatalf("the no-plan message was lost:\n%s", out)
	}
}
