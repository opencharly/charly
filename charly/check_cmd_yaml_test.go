package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestEmitImageTestYAML_RoundTripsThroughParseCharlyTestOutput is the
// load-bearing invariant: whatever `charly check box --format yaml`
// emits MUST parse cleanly via ParseCharlyTestOutput. Without this,
// the benchmark scorer would silently mis-parse and classify steps
// wrong. Only check:/agent-check: steps land in the scored payload;
// run: steps (the install timeline) are excluded.
func TestEmitImageTestYAML_RoundTripsThroughParseCharlyTestOutput(t *testing.T) {
	steps := []StepResult{
		{
			Keyword: string(KwCheck),
			Text:    "sshd is installed",
			Origin:  "candy:sshd",
			StepID:  "plan:candy:sshd:0",
			Result:  CheckResult{Verb: "package", Status: TestPass},
		},
		{
			Keyword: string(KwCheck),
			Text:    "port reachable",
			Origin:  "candy:sshd",
			StepID:  "plan:candy:sshd:1",
			Result:  CheckResult{Verb: "port", Status: TestPass},
		},
		{
			Keyword: string(KwCheck),
			Text:    "foo service runs",
			Origin:  "candy:foo",
			StepID:  "plan:candy:foo:0",
			Result:  CheckResult{Verb: "service", Status: TestFail},
		},
		{
			// A run: step is NOT scored — it must be excluded from the payload.
			Keyword: string(KwRun),
			Text:    "install foo",
			Origin:  "candy:foo",
			StepID:  "plan:candy:foo:1",
			Result:  CheckResult{Verb: "package", Status: TestPass},
		},
	}

	var buf bytes.Buffer
	if err := emitImageTestYAML(&buf, "ovbench/test:charly-fedora", "", steps, nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "box: ovbench/test:charly-fedora") {
		t.Errorf("missing box line: %q", out)
	}
	if !strings.Contains(out, "mode: box") {
		t.Errorf("missing mode: %q", out)
	}

	// Round-trip through the benchmark parser.
	parsed, err := ParseCharlyTestOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCharlyTestOutput failed on emitted YAML: %v\n%s", err, out)
	}
	if parsed.Box != "ovbench/test:charly-fedora" {
		t.Errorf("parsed box: %q", parsed.Box)
	}
	if parsed.Mode != "box" {
		t.Errorf("parsed mode: %q", parsed.Mode)
	}
	// 3 scored check: steps; the run: step is excluded.
	if len(parsed.Step) != 3 {
		t.Fatalf("want 3 scored steps (run: excluded), got %d", len(parsed.Step))
	}
	if parsed.Step[0].ID != "plan:candy:sshd:0" {
		t.Errorf("step[0].ID: %q", parsed.Step[0].ID)
	}
	if parsed.Step[0].Status != "pass" {
		t.Errorf("step[0].Status: %q", parsed.Step[0].Status)
	}
	if parsed.Step[2].Status != "fail" {
		t.Errorf("step[2].Status: %q", parsed.Step[2].Status)
	}
	// Summary derivation (producer set totals).
	if parsed.Summary.Total != 3 || parsed.Summary.Pass != 2 || parsed.Summary.Fail != 1 {
		t.Errorf("summary: %+v", parsed.Summary)
	}
}

func TestEmitImageTestYAML_LiveContainerMode(t *testing.T) {
	var buf bytes.Buffer
	if err := emitImageTestYAML(&buf, "ref:tag", "charly-fedora-coder", nil, nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(buf.String(), "mode: run") {
		t.Errorf("live container should emit mode: run; got %q", buf.String())
	}
}

func TestEmitImageTestYAML_EmptyScenariosEmitsUsableYAML(t *testing.T) {
	var buf bytes.Buffer
	if err := emitImageTestYAML(&buf, "ref:tag", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCharlyTestOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if parsed.Summary.Total != 0 {
		t.Errorf("empty should yield total=0: %+v", parsed.Summary)
	}
}
