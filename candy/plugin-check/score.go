package check

// score.go — scoring primitives for the AI harness (P12: relocated from
// charly/check_score.go + the pure parts of charly/check_score_kind.go +
// synthesizeScoreBaseline from charly/check_runner_live.go).
//
// The scoring unit is the check:/agent-check: STEP, keyed by step id
// (kit.EffectiveStepID). The scoring result model — spec.CheckRunResults /
// spec.StepScore / spec.ScoreSummary — is a CUE-sourced sdk WIRE TYPE: ONE
// definition serves both the "score" host seam reply (kit.CheckRunReply.Score) AND
// this plugin scorer (no alias). RunCheckLive itself STAYS core (registry/venue
// coupled) and is reached via the "score" check-run mode; only the pure
// parser/classifier/baseline math lives here.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// scoredPlanOrigin is the fixed origin used to derive step ids so that
// synthesizeScoreBaseline and the host "score"-mode RunCheckLive produce matching
// ids.
const scoredPlanOrigin = "plan"

// isScored reports whether a step is a scored success criterion (check: or
// agent-check:).
func isScored(s spec.Step) bool { return s.Check != "" || s.AgentCheck != "" }

// ---------------------------------------------------------------------------
// `charly check box --format yaml` parser
// ---------------------------------------------------------------------------

// ParseCharlyTestOutput parses the byte slice emitted by `charly check box <tag>
// --format yaml` into a *spec.CheckRunResults. Empty input → empty result.
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

// stepScoresByID builds a map from step id to its scorer result.
func stepScoresByID(r *spec.CheckRunResults) map[string]spec.StepScore {
	out := make(map[string]spec.StepScore)
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

// FingerprintStep returns a stable SHA256 fingerprint of a Step's semantic content.
// Deterministic; insensitive to tag ordering; sensitive to keyword prose + every
// embedded Op field. Output: "sha256:<64 hex>".
func FingerprintStep(s spec.Step) string {
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
func FingerprintSet(set *kit.LabelDescriptionSet) map[string]string {
	out := make(map[string]string)
	if set == nil {
		return out
	}
	for _, sec := range [][]kit.LabeledDescription{set.Candy, set.Box, set.Deploy} {
		for _, ld := range sec {
			for sIdx, step := range ld.Plan {
				id := kit.EffectiveStepID(&step, ld.Origin, sIdx)
				out[id] = FingerprintStep(step)
			}
		}
	}
	return out
}

// collectTagFingerprints returns every step's tag fingerprint keyed by step id.
func collectTagFingerprints(set *kit.LabelDescriptionSet) map[string]string {
	out := make(map[string]string)
	if set == nil {
		return out
	}
	for _, sec := range [][]kit.LabeledDescription{set.Candy, set.Box, set.Deploy} {
		for _, ld := range sec {
			for sIdx, step := range ld.Plan {
				id := kit.EffectiveStepID(&step, ld.Origin, sIdx)
				out[id] = FingerprintTags(step.Tag)
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

// StepState summarizes one step's state at a point in time (baseline or
// post-iteration). Present==false means the step was absent from that set.
type StepState struct {
	Present        bool
	Fingerprint    string // "" when !Present
	Status         string // "pass" | "fail" | "skip" | "skipped" | "" (not run)
	TagFingerprint string
}

// Classify compares a step's baseline state to its post-iteration state and returns
// the verdict.
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

// ---------------------------------------------------------------------------
// Pre-AI baseline synthesis (from charly/check_runner_live.go)
// ---------------------------------------------------------------------------

// synthesizeScoreBaseline builds the pre-AI baseline from the scored steps, marking
// each check:/agent-check: step status: fail at baseline. IDs match the
// declaration-order ids the host "score"-mode RunCheckLive emits.
func synthesizeScoreBaseline(scoreName string, plan []spec.Step) ([]spec.StepScore, map[string]string, map[string]string) {
	_ = scoreName
	var out []spec.StepScore
	fps := make(map[string]string)
	tagFps := make(map[string]string)
	for i := range plan {
		s := plan[i]
		if !isScored(s) {
			continue
		}
		id := kit.EffectiveStepID(&s, scoredPlanOrigin, i)
		out = append(out, spec.StepScore{
			ID:      id,
			Origin:  "pod:" + s.Venue,
			Text:    s.KeywordText(),
			Tag:     kit.EffectiveTags(s.Tag),
			Keyword: string(kit.KeywordOf(&s)),
			Status:  "fail",
		})
		fps[id] = FingerprintStep(s)
		tagFps[id] = FingerprintTags(s.Tag)
	}
	return out, fps, tagFps
}

// ---------------------------------------------------------------------------
// iterate: MCP endpoint (from charly/check_score_kind.go)
// ---------------------------------------------------------------------------

// DefaultMCPEndpoint is the canonical charly-mcp bind URL.
const DefaultMCPEndpoint = "http://localhost:18765/mcp"

// iterateEffectiveMCPEndpoint resolves the canonical ${MCP_ENDPOINT} value: a nil
// endpoint (unset) → DefaultMCPEndpoint; a non-nil "" → "" (disabled by author); a
// non-nil value → that value verbatim.
func iterateEffectiveMCPEndpoint(i *spec.Iterate) string {
	if i == nil || i.MCPEndpoint == nil {
		return DefaultMCPEndpoint
	}
	return *i.MCPEndpoint
}
