package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// --- parseVerdict --------------------------------------------------------

func TestParseVerdict_Plain(t *testing.T) {
	pass, ev, ok := parseVerdict("some reasoning here\n" + `{"verdict":"pass","evidence":"the port answered PONG"}`)
	if !ok || !pass || ev != "the port answered PONG" {
		t.Fatalf("got pass=%v ev=%q ok=%v", pass, ev, ok)
	}
}

func TestParseVerdict_Fail(t *testing.T) {
	pass, _, ok := parseVerdict(`{"verdict":"fail","evidence":"connection refused"}`)
	if !ok || pass {
		t.Fatalf("got pass=%v ok=%v, want fail", pass, ok)
	}
}

func TestParseVerdict_StreamJSON(t *testing.T) {
	out := `{"type":"system","subtype":"init"}` + "\n" +
		`{"type":"assistant","message":"checking..."}` + "\n" +
		`{"type":"result","subtype":"success","result":"I probed it. {\"verdict\":\"pass\",\"evidence\":\"ok\"}"}`
	pass, ev, ok := parseVerdict(out)
	if !ok || !pass || ev != "ok" {
		t.Fatalf("stream-json: pass=%v ev=%q ok=%v", pass, ev, ok)
	}
}

func TestParseVerdict_NoVerdict(t *testing.T) {
	if _, _, ok := parseVerdict("just prose, no json object at all"); ok {
		t.Fatal("expected no parseable verdict")
	}
}

func TestParseVerdict_LastWins(t *testing.T) {
	// An earlier illustrative example must not beat the final real verdict.
	out := `for example {"verdict":"fail"} but actually {"verdict":"pass","evidence":"done"}`
	pass, ev, ok := parseVerdict(out)
	if !ok || !pass || ev != "done" {
		t.Fatalf("last-wins: pass=%v ev=%q ok=%v", pass, ev, ok)
	}
}

// --- RunAgentOnce -----------------------------------------------------------

func TestRunAIOnce_CapturesStdout(t *testing.T) {
	ai := &spec.AgentExecSpec{Command: []string{"sh", "-c", `echo '{"verdict":"pass","evidence":"ok"}'`}}
	out, _, err := RunAgentOnce(context.Background(), ai, "ignored", 10*time.Second)
	if err != nil {
		t.Fatalf("RunAgentOnce: %v", err)
	}
	if pass, _, ok := parseVerdict(out); !ok || !pass {
		t.Fatalf("verdict not parsed from %q", out)
	}
}

func TestRunAIOnce_SubstitutesPrompt(t *testing.T) {
	ai := &spec.AgentExecSpec{Command: []string{"printf", "%s", "${PROMPT}"}}
	out, _, err := RunAgentOnce(context.Background(), ai, "HELLO-PROMPT-TOKEN", 10*time.Second)
	if err != nil {
		t.Fatalf("RunAgentOnce: %v", err)
	}
	if !strings.Contains(out, "HELLO-PROMPT-TOKEN") {
		t.Fatalf("${PROMPT} not substituted into argv: %q", out)
	}
}

func TestRunAIOnce_Timeout(t *testing.T) {
	ai := &spec.AgentExecSpec{Command: []string{"sleep", "10"}}
	_, _, err := RunAgentOnce(context.Background(), ai, "x", 150*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestRunAIOnce_NoCommand(t *testing.T) {
	if _, _, err := RunAgentOnce(context.Background(), &spec.AgentExecSpec{}, "x", time.Second); err == nil {
		t.Fatal("expected error for an ai entry with no command")
	}
}

// --- AgentGrader ---------------------------------------------------------

func TestAgentGrader_GradeFail(t *testing.T) {
	ai := &spec.AgentExecSpec{Command: []string{"sh", "-c", `echo '{"verdict":"fail","evidence":"port closed"}'`}}
	g := &AgentGrader{Agent: ai, Target: "check-pod"}
	res := g.Grade(context.Background(), GraderRequest{Keyword: "Then", Text: "the port answers"})
	if res.Status != TestFail {
		t.Fatalf("want TestFail, got %v", res.Status)
	}
	if !strings.Contains(res.Message, "port closed") {
		t.Fatalf("evidence not surfaced: %q", res.Message)
	}
}

func TestAgentGrader_UnparseableIsFail(t *testing.T) {
	ai := &spec.AgentExecSpec{Command: []string{"sh", "-c", `echo "I have no idea"`}}
	g := &AgentGrader{Agent: ai, Target: "check-pod"}
	res := g.Grade(context.Background(), GraderRequest{Keyword: "Then", Text: "x"})
	if res.Status != TestFail {
		t.Fatalf("unparseable grader output must FAIL (never silent pass), got %v", res.Status)
	}
}

// --- grader dispatch through RunPlan -------------------------------------

type stubGrader struct {
	pass    bool
	calls   int
	lastReq GraderRequest
}

func (g *stubGrader) Grade(_ context.Context, req GraderRequest) CheckResult {
	g.calls++
	g.lastReq = req
	st := TestFail
	if g.pass {
		st = TestPass
	}
	return CheckResult{Status: st, Verb: "agent", Message: "stub"}
}

// proseAgentSet returns a label set whose single candy plan has one prose-only
// agent-check: step (no Op verb) — the grader-dispatch path.
func proseAgentSet() *LabelDescriptionSet {
	return &LabelDescriptionSet{
		Candy: []LabeledDescription{{
			Origin:      "candy:x",
			Description: "the gizmo works",
			Plan:        []Step{{AgentCheck: "the gizmo responds"}},
		}},
	}
}

func TestRunPlan_GraderDispatchPass(t *testing.T) {
	g := &stubGrader{pass: true}
	r := newCheckRunner(kit.RunnerConfig{Mode: RunModeLive, Grader: g})
	res := RunPlan(context.Background(), r, proseAgentSet(), nil, false)
	if len(res) != 1 || res[0].Result.Status != TestPass {
		t.Fatalf("graded agent step should pass, got %+v", res)
	}
	if g.calls != 1 {
		t.Fatalf("grader called %d times, want 1", g.calls)
	}
	// Goal (entity description) + step context threaded into the grader.
	if g.lastReq.Description != "the gizmo works" || g.lastReq.Keyword != string(KwAgentCheck) || g.lastReq.Text != "the gizmo responds" {
		t.Fatalf("grader request context not threaded: %+v", g.lastReq)
	}
}

func TestRunPlan_GraderDispatchFail(t *testing.T) {
	r := newCheckRunner(kit.RunnerConfig{Mode: RunModeLive, Grader: &stubGrader{pass: false}})
	res := RunPlan(context.Background(), r, proseAgentSet(), nil, false)
	if len(res) != 1 || res[0].Result.Status != TestFail {
		t.Fatalf("a failing grader must fail the agent step, got %+v", res)
	}
}

func TestRunPlan_NoGrader_ProseSkips(t *testing.T) {
	r := newCheckRunner(kit.RunnerConfig{Mode: RunModeLive}) // no grader
	res := RunPlan(context.Background(), r, proseAgentSet(), nil, false)
	if len(res) != 1 {
		t.Fatalf("want 1 step result, got %d", len(res))
	}
	if res[0].Result.Status == TestFail {
		t.Fatalf("a prose agent step without a grader must NOT fail (advisory skip), got %v", res[0].Result.Status)
	}
	if res[0].Result.Status != TestSkip {
		t.Fatalf("a prose agent step without a grader should skip, got %v", res[0].Result.Status)
	}
}

// --- stepFailCount -------------------------------------------------------

func TestStepFailCount(t *testing.T) {
	in := []StepResult{
		{Result: CheckResult{Status: TestPass}},
		{Result: CheckResult{Status: TestFail}},
		{Result: CheckResult{Status: TestSkip}},
		{Result: CheckResult{Status: TestFail}},
	}
	if got := stepFailCount(in); got != 2 {
		t.Fatalf("stepFailCount = %d, want 2", got)
	}
}

// --- buildGraderPrompt ---------------------------------------------------

// TestBuildGraderPrompt_PillarName is the check-coverage gate for the grader
// system prompt naming the ADE pillar ("Agent Driven Evaluation").
func TestBuildGraderPrompt_PillarName(t *testing.T) {
	prompt := buildGraderPrompt(GraderRequest{Keyword: "agent-check", Text: "the port answers"}, "check-pod", "")
	if !strings.Contains(prompt, "Agent Driven Evaluation") {
		t.Fatalf("grader prompt must name the pillar 'Agent Driven Evaluation'; got:\n%s", prompt)
	}
}
