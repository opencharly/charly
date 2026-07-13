package check

// substitute_test.go — coverage for the ${TOKEN} harness substitution, relocated
// from the deleted core duplicate (charly/check_substitute_test.go) onto the LIVE
// plugin twin (substitute.go) in the K5 check-residue dedup (R3/R5). The core
// copy was a dead duplicate with zero callers; these tests now exercise the one
// live Substitute / SubstContext / SubstituteArgv.

import (
	"os"
	"strings"
	"testing"
)

func TestSubstitute_WellKnownTokens(t *testing.T) {
	ctx := &SubstContext{
		RunID:            "run-1",
		ScoreName:        "default",
		AgentName:        "claude",
		WorkspacePath:    "/workspace/repo",
		TargetImage:      "fedora-coder",
		TargetKind:       "pod",
		TargetName:       "sample-pod",
		Iteration:        2,
		PlateauIteration: 3,
		PlateauCounter:   1,
		BestScore:        4,
		ScoreDelta:       2,
		AttemptsLeft:     2,
		MCPEndpoint:      "http://mcp.example/",
		Notes:            "remember this",
		Plan:             "- check: tier1\n",
		Tag:              "@smoke",
	}
	cases := map[string]string{
		"${RUN_ID}":            "run-1",
		"${SCORE_NAME}":        "default",
		"${AI_NAME}":           "claude",
		"${WORKSPACE}":         "/workspace/repo",
		"${TARGET_IMAGE}":      "fedora-coder",
		"${TARGET_KIND}":       "pod",
		"${TARGET_NAME}":       "sample-pod",
		"${ITERATION}":         "2",
		"${PLATEAU_ITERATION}": "3",
		"${PLATEAU_COUNTER}":   "1",
		"${BEST_SCORE}":        "4",
		"${SCORE_DELTA}":       "2",
		"${ATTEMPTS_LEFT}":     "2",
		"${MCP_ENDPOINT}":      "http://mcp.example/",
		"${NOTES}":             "remember this",
		"${PLAN}":              "- check: tier1\n",
		"${TAG}":               "@smoke",
	}
	for in, want := range cases {
		if got := Substitute(in, ctx); got != want {
			t.Errorf("Substitute(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubstitute_RemovedTokens(t *testing.T) {
	// ${RECIPE_NAME} and ${MAX_ITERATION} are removed tokens. Tokens that aren't
	// well-known fall through the env chain and finally to os.Getenv. Without any
	// binding they resolve to "".
	t.Setenv("RECIPE_NAME", "")
	t.Setenv("MAX_ITERATION", "")
	ctx := &SubstContext{ScoreName: "default"}
	if got := Substitute("${RECIPE_NAME}", ctx); got != "" {
		t.Errorf("removed ${RECIPE_NAME} should resolve empty, got %q", got)
	}
	if got := Substitute("${MAX_ITERATION}", ctx); got != "" {
		t.Errorf("removed ${MAX_ITERATION} should resolve empty, got %q", got)
	}
}

func TestSubstitute_PrecedenceChain(t *testing.T) {
	t.Setenv("MY_VAR", "from-env")
	scoreEnv := map[string]string{"MY_VAR": "from-score"}
	aiEnv := map[string]string{"MY_VAR": "from-ai"}

	// score.env wins over ai.env wins over os.Getenv
	ctx := &SubstContext{}
	ctx.AppendEnv(scoreEnv)
	ctx.AppendEnv(aiEnv)
	if got := Substitute("${MY_VAR}", ctx); got != "from-score" {
		t.Errorf("score.env should win, got %q", got)
	}

	ctx2 := &SubstContext{}
	ctx2.AppendEnv(aiEnv)
	if got := Substitute("${MY_VAR}", ctx2); got != "from-ai" {
		t.Errorf("ai.env should win when score absent, got %q", got)
	}

	ctx3 := &SubstContext{}
	if got := Substitute("${MY_VAR}", ctx3); got != "from-env" {
		t.Errorf("os.Getenv should be last resort, got %q", got)
	}

	if got := Substitute("${UNSET_TOKEN_XYZ}", ctx3); got != "" {
		t.Errorf("unresolved token should be empty, got %q", got)
	}
}

func TestSubstitute_SinglePass(t *testing.T) {
	ctx := &SubstContext{}
	ctx.AppendEnv(map[string]string{"OUTER": "${INNER}", "INNER": "leaf"})
	if got := Substitute("${OUTER}", ctx); got != "${INNER}" {
		t.Errorf("expected single-pass substitution; got %q (recursion happened)", got)
	}
}

func TestSubstituteArgv_NoMutation(t *testing.T) {
	ctx := &SubstContext{Prompt: "hello"}
	argv := []string{"echo", "${PROMPT}"}
	out := SubstituteArgv(argv, ctx)
	if out[1] != "hello" {
		t.Errorf("expected substitution, got %q", out[1])
	}
	if argv[1] != "${PROMPT}" {
		t.Error("input argv was mutated")
	}
}

func TestSubstitute_OSEnvFallthrough(t *testing.T) {
	got := os.Getenv("PATH")
	if got == "" {
		t.Skip("no PATH in env")
	}
	ctx := &SubstContext{}
	if want := strings.Split(got, ":"); len(want) > 0 {
		if Substitute("${PATH}", ctx) == "" {
			t.Error("PATH fallback failed")
		}
	}
}
