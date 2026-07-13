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

	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// `charly check box --format yaml` parser
// ---------------------------------------------------------------------------
//
// The scoring result model — spec.CheckRunResults / spec.StepScore / spec.ScoreSummary — is a
// CUE-sourced sdk WIRE TYPE (sdk/schema/seam.cue, SDD): ONE definition serves both this core
// scorer AND the relocated plugin scorer (no alias). The pure parser/classifier helpers below
// (ParseCharlyTestOutput / Classify / FingerprintSet / Verdict) STAY in core (they have core
// callers).

// ParseCharlyTestOutput parses the byte slice emitted by `charly check box
// <tag> --format yaml` into a *spec.CheckRunResults. Empty input → empty result.
func ParseCharlyTestOutput(b []byte) (*spec.CheckRunResults, error) {
	if len(b) == 0 {
		return &spec.CheckRunResults{}, nil
	}
	var r spec.CheckRunResults
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

// deriveSummary computes a spec.ScoreSummary from step-level statuses.
func deriveSummary(steps []spec.StepScore) spec.ScoreSummary {
	var s spec.ScoreSummary
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
