package check

// score_test.go — coverage for the LIVE harness scorer (score.go). Written in the
// K5 check-residue dedup (R3/R5): the deleted core duplicate (charly/check_score.go)
// shipped ZERO tests, so this closes the P12 coverage gap on the one live scorer —
// the verdict matrix (Classify), the deterministic fingerprints, and the
// check-box-output parser + summary derivation.

import (
	"testing"

	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

func TestClassify_VerdictMatrix(t *testing.T) {
	const (
		fpA = "sha256:aaa"
		fpB = "sha256:bbb"
		tgA = "sha256:tagA"
		tgB = "sha256:tagB"
	)
	cases := []struct {
		name string
		pre  StepState
		post StepState
		want Verdict
	}{
		{"skipped wins over everything",
			StepState{Present: true, Status: "pass", Fingerprint: fpA},
			StepState{Present: true, Status: "skipped", Fingerprint: fpA}, VerdictSkipped},
		{"absent→present is added",
			StepState{Present: false},
			StepState{Present: true, Status: "pass", Fingerprint: fpA}, VerdictAdded},
		{"present→absent is tampered",
			StepState{Present: true, Status: "pass", Fingerprint: fpA},
			StepState{Present: false}, VerdictTampered},
		{"body changed + passing is tampered",
			StepState{Present: true, Status: "fail", Fingerprint: fpA},
			StepState{Present: true, Status: "pass", Fingerprint: fpB}, VerdictTampered},
		{"tags-only change is retagged",
			StepState{Present: true, Status: "pass", Fingerprint: fpA, TagFingerprint: tgA},
			StepState{Present: true, Status: "pass", Fingerprint: fpA, TagFingerprint: tgB}, VerdictRetagged},
		{"pass→fail is regressed",
			StepState{Present: true, Status: "pass", Fingerprint: fpA},
			StepState{Present: true, Status: "fail", Fingerprint: fpA}, VerdictRegressed},
		{"fail→pass with unchanged body is solved",
			StepState{Present: true, Status: "fail", Fingerprint: fpA},
			StepState{Present: true, Status: "pass", Fingerprint: fpA}, VerdictSolved},
		{"empty baseline→pass unchanged body is solved",
			StepState{Present: true, Status: "", Fingerprint: fpA},
			StepState{Present: true, Status: "pass", Fingerprint: fpA}, VerdictSolved},
		{"pass→pass unchanged is unchanged",
			StepState{Present: true, Status: "pass", Fingerprint: fpA},
			StepState{Present: true, Status: "pass", Fingerprint: fpA}, VerdictUnchanged},
	}
	for _, c := range cases {
		if got := Classify(c.pre, c.post); got != c.want {
			t.Errorf("%s: Classify() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFingerprintTags_DeterministicAndOrderInsensitive(t *testing.T) {
	a := FingerprintTags([]string{"b", "a", "c"})
	b := FingerprintTags([]string{"c", "b", "a"})
	if a != b {
		t.Errorf("FingerprintTags is order-sensitive: %q != %q", a, b)
	}
	if a == FingerprintTags([]string{"a", "b"}) {
		t.Error("distinct tag sets share a fingerprint")
	}
	if len(a) < 7 || a[:7] != "sha256:" {
		t.Errorf("fingerprint not sha256-prefixed: %q", a)
	}
	if FingerprintTags(nil) != FingerprintTags([]string{}) {
		t.Error("nil and empty tag sets must fingerprint identically")
	}
}

func TestParseCharlyTestOutput_EmptyAndSummaryDerivation(t *testing.T) {
	// Empty input → empty result, no error.
	r, err := ParseCharlyTestOutput(nil)
	if err != nil {
		t.Fatalf("empty parse: %v", err)
	}
	if r == nil || r.Summary.Total != 0 || len(r.Step) != 0 {
		t.Fatalf("empty parse should be zero-valued, got %+v", r)
	}

	// Steps with a zero Summary → deriveSummary fills pass/fail/skip counts.
	// Marshal a well-formed result (all KnownFields) so re-parsing is robust.
	in := &spec.CheckRunResults{
		Step: []spec.StepScore{
			{ID: "s1", Status: "pass"},
			{ID: "s2", Status: "fail"},
			{ID: "s3", Status: "skip"},
			{ID: "s4", Status: "pass"},
		},
	}
	b, err := yaml.Marshal(in)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	got, err := ParseCharlyTestOutput(b)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if got.Summary.Total != 4 || got.Summary.Pass != 2 || got.Summary.Fail != 1 || got.Summary.Skip != 1 {
		t.Fatalf("deriveSummary wrong: total=%d pass=%d fail=%d skip=%d",
			got.Summary.Total, got.Summary.Pass, got.Summary.Fail, got.Summary.Skip)
	}
}
