package main

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveAI_NoAIs(t *testing.T) {
	if _, _, err := ResolveAgent(nil, ""); !errors.Is(err, ErrNoAgents) {
		t.Errorf("expected ErrNoAgents, got %v", err)
	}
	if _, _, err := ResolveAgent(map[string]*AgentConfig{}, ""); !errors.Is(err, ErrNoAgents) {
		t.Errorf("expected ErrNoAgents for empty map, got %v", err)
	}
}

func TestResolveAI_SoleAIImplicit(t *testing.T) {
	cat := map[string]*AgentConfig{
		"claude": {Command: []string{"claude"}},
	}
	ai, name, err := ResolveAgent(cat, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "claude" {
		t.Errorf("name=%q, want claude", name)
	}
	// DefaultAgentTimeout is "" (no wall-clock cap); the runner uses
	// context.WithCancel instead of context.WithTimeout when empty.
	// Plateau detection is the loop bound, not wall clock.
	if ai.Timeout != DefaultAgentTimeout {
		t.Errorf("default timeout not applied: got %q (want empty)", ai.Timeout)
	}
	if ai.Timeout != "" {
		t.Errorf("expected empty Timeout default; got %q", ai.Timeout)
	}
	if ai.PromptVia != "argv" {
		t.Errorf("default prompt_via not applied: got %q", ai.PromptVia)
	}
}

func TestResolveAI_MultipleAmbiguous(t *testing.T) {
	cat := map[string]*AgentConfig{
		"a": {Command: []string{"a"}},
		"b": {Command: []string{"b"}},
	}
	_, _, err := ResolveAgent(cat, "")
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "--agent NAME") {
		t.Errorf("error message should suggest --agent; got: %s", err)
	}
}

func TestResolveAI_NotFound(t *testing.T) {
	cat := map[string]*AgentConfig{"claude": {Command: []string{"claude"}}}
	_, _, err := ResolveAgent(cat, "missing")
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestSortedAINames(t *testing.T) {
	cat := map[string]*AgentConfig{
		"zebra": {},
		"alpha": {},
		"mike":  {},
	}
	got := SortedAgentNames(cat)
	want := []string{"alpha", "mike", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d] got %q, want %q", i, got[i], w)
		}
	}
}

func TestVersionResultString(t *testing.T) {
	v := VersionResult{Stdout: "claude 0.1.2"}
	if v.String() != "claude 0.1.2" {
		t.Errorf("got %q", v.String())
	}
	v2 := VersionResult{Error: "command not found"}
	if v2.String() != "error: command not found" {
		t.Errorf("got %q", v2.String())
	}
}

func TestParseAITimeout(t *testing.T) {
	// Empty timeout → 0 (no wall-clock cap). harness_loop branches on
	// `dur == 0` and uses context.WithCancel instead of WithTimeout,
	// honoring the "Take all the time you need" prompt promise.
	d, err := ParseAgentTimeout("")
	if err != nil {
		t.Fatalf("default (empty) timeout failed: %v", err)
	}
	if d != 0 {
		t.Errorf("default timeout should be 0 (no cap); got %v", d)
	}
	// Explicit cap still works for authors who want one.
	d2, err := ParseAgentTimeout("5m")
	if err != nil || d2.Minutes() != 5 {
		t.Errorf("explicit timeout: got %v, err=%v", d2, err)
	}
	if _, err := ParseAgentTimeout("nope"); err == nil {
		t.Error("invalid duration should error")
	}
}
