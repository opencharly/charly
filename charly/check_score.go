package main

// check_score.go — scoring primitives for `charly check`.
//
// Post the plan-unify cutover the scoring unit is the check:/agent-check:
// STEP, keyed by step id (EffectiveStepID). Exported surface:
//   - ParseCharlyTestOutput(yaml) (*CheckRunResults, error)
//   - FingerprintStep(s Step) string
//   - FingerprintSet(set *LabelDescriptionSet) map[string]string  (id → fp)
//   - Classify(pre, post StepState) Verdict

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// `charly check box --format yaml` parser
// ---------------------------------------------------------------------------

// CheckRunResults is the structured result of one `charly check box` run, as
// produced by `--format yaml`. The zero value is a usable empty result.
type CheckRunResults struct {
	Box     string         `yaml:"box,omitempty" json:"box,omitempty"`
	Mode    string         `yaml:"mode,omitempty" json:"mode,omitempty"` // "box" | "run"
	Step    []StepScore    `yaml:"step,omitempty" json:"step,omitempty"`
	Summary TestRunSummary `yaml:"summary" json:"summary"`
}

// StepScore is the scorer's verdict for a single check:/agent-check: step,
// keyed by step id across iterations for plateau tracking.
type StepScore struct {
	ID      string   `yaml:"id" json:"id"`
	Origin  string   `yaml:"origin,omitempty" json:"origin,omitempty"`
	Text    string   `yaml:"text,omitempty" json:"text,omitempty"`
	Tag     []string `yaml:"tag,omitempty" json:"tag,omitempty"`
	Keyword string   `yaml:"keyword,omitempty" json:"keyword,omitempty"`
	Verb    string   `yaml:"verb,omitempty" json:"verb,omitempty"`
	Status  string   `yaml:"status" json:"status"` // "pass" | "fail" | "skip" | "skipped"
	// SkippedReason is set when Status == "skipped" — the depends_on upstream
	// that didn't pass. Format: "dep-unmet: <upstream-id>".
	SkippedReason string `yaml:"skipped_reason,omitempty" json:"skipped_reason,omitempty"`
}

// TestRunSummary mirrors the summary block emitted by `charly check box
// --format yaml`.
type TestRunSummary struct {
	Total int `yaml:"total" json:"total"`
	Pass  int `yaml:"pass" json:"pass"`
	Fail  int `yaml:"fail" json:"fail"`
	Skip  int `yaml:"skip" json:"skip"`
}

// ParseCharlyTestOutput parses the byte slice emitted by `charly check box
// <tag> --format yaml` into a *CheckRunResults. Empty input → empty result.
func ParseCharlyTestOutput(b []byte) (*CheckRunResults, error) {
	if len(b) == 0 {
		return &CheckRunResults{}, nil
	}
	var r CheckRunResults
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("parse charly check box --format yaml: %w", err)
	}
	if r.Summary.Total == 0 && len(r.Step) > 0 {
		r.Summary = deriveSummary(r.Step)
	}
	return &r, nil
}

// deriveSummary computes TestRunSummary from step-level statuses.
func deriveSummary(steps []StepScore) TestRunSummary {
	var s TestRunSummary
	for _, st := range steps {
		s.Total++
		switch st.Status {
		case "pass":
			s.Pass++
		case "fail":
			s.Fail++
		case "skip", "skipped":
			s.Skip++
		}
	}
	return s
}

// StepByID builds a map from step id to its scorer result.
func (r *CheckRunResults) StepByID() map[string]StepScore {
	out := make(map[string]StepScore)
	if r == nil {
		return out
	}
	for _, st := range r.Step {
		out[st.ID] = st
	}
	return out
}

// ---------------------------------------------------------------------------
// Fingerprinting
// ---------------------------------------------------------------------------

// FingerprintStep returns a stable SHA256 fingerprint of a Step's semantic
// content. Deterministic; insensitive to tag ordering; sensitive to keyword
// prose + every embedded Op field. Output: "sha256:<64 hex>".
func FingerprintStep(s Step) string {
	clone := s
	clone.Tag = append([]string(nil), s.Tag...)
	sort.Strings(clone.Tag)
	out, err := yaml.Marshal(clone)
	if err != nil {
		return fmt.Sprintf("MARSHAL_ERR:%s", clone.KeywordText())
	}
	sum := sha256.Sum256(out)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FingerprintSet returns every step in the set keyed by step id.
func FingerprintSet(set *LabelDescriptionSet) map[string]string {
	out := make(map[string]string)
	if set == nil {
		return out
	}
	for _, sec := range [][]LabeledDescription{set.Candy, set.Box, set.Deploy} {
		for _, ld := range sec {
			for sIdx, step := range ld.Plan {
				id := EffectiveStepID(&step, ld.Origin, sIdx)
				out[id] = FingerprintStep(step)
			}
		}
	}
	return out
}

// FingerprintTags returns a canonical SHA256 over the sorted tag set.
func FingerprintTags(tags []string) string {
	sorted := append([]string(nil), tags...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// Verdict classification
// ---------------------------------------------------------------------------

// Verdict is the classification of a step's trajectory from pre-AI baseline to
// post-iteration state. Values are the YAML string representation.
type Verdict string

const (
	VerdictSolved    Verdict = "solved"
	VerdictUnchanged Verdict = "unchanged"
	VerdictRegressed Verdict = "regressed"
	VerdictTampered  Verdict = "tampered"
	VerdictRetagged  Verdict = "retagged"
	VerdictAdded     Verdict = "added"
	VerdictSkipped   Verdict = "skipped"
)

// AllVerdicts lists every verdict in reporting order.
var AllVerdicts = []Verdict{
	VerdictSolved, VerdictUnchanged, VerdictRegressed,
	VerdictTampered, VerdictRetagged, VerdictAdded, VerdictSkipped,
}

// StepState summarizes one step's state at a point in time (baseline or
// post-iteration). Present==false means the step was absent from that set.
type StepState struct {
	Present        bool
	Fingerprint    string // "" when !Present
	Status         string // "pass" | "fail" | "skip" | "skipped" | "" (not run)
	TagFingerprint string
}

// Classify compares a step's baseline state to its post-iteration state and
// returns the verdict.
func Classify(pre, post StepState) Verdict {
	if post.Status == "skipped" {
		return VerdictSkipped
	}
	if !pre.Present && post.Present {
		return VerdictAdded
	}
	if pre.Present && !post.Present {
		return VerdictTampered
	}

	bodyChanged := pre.Fingerprint != "" && post.Fingerprint != "" && pre.Fingerprint != post.Fingerprint
	tagsChanged := pre.TagFingerprint != "" && post.TagFingerprint != "" && pre.TagFingerprint != post.TagFingerprint

	if bodyChanged && post.Status == "pass" {
		return VerdictTampered
	}
	if !bodyChanged && tagsChanged {
		return VerdictRetagged
	}
	if pre.Status == "pass" && post.Status == "fail" {
		return VerdictRegressed
	}
	baselineNotPassing := pre.Status == "fail" || pre.Status == "skip" || pre.Status == ""
	if baselineNotPassing && post.Status == "pass" && !bodyChanged {
		return VerdictSolved
	}
	return VerdictUnchanged
}
