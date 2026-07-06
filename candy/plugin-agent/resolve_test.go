package agentkind

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

func agentBody(t *testing.T, a spec.Agent) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal agent: %v", err)
	}
	return b
}

func TestResolveAgent_NoAgents(t *testing.T) {
	if _, err := resolveAgent(spec.AgentResolveInput{}); err == nil {
		t.Error("expected error for no agents")
	}
	if _, err := resolveAgent(spec.AgentResolveInput{Agents: map[string]json.RawMessage{}}); err == nil {
		t.Error("expected error for empty catalog")
	}
}

func TestResolveAgent_SoleImplicit(t *testing.T) {
	in := spec.AgentResolveInput{Agents: map[string]json.RawMessage{
		"claude": agentBody(t, spec.Agent{Command: []string{"claude"}}),
	}}
	reply, err := resolveAgent(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply.Name != "claude" {
		t.Errorf("name=%q, want claude", reply.Name)
	}
	// DefaultAgentTimeout is "" (no wall-clock cap); plateau detection is the bound.
	if reply.Spec.Timeout != "" {
		t.Errorf("default timeout not empty: got %q", reply.Spec.Timeout)
	}
	if reply.Spec.PromptVia != "argv" {
		t.Errorf("default prompt_via not applied: got %q", reply.Spec.PromptVia)
	}
}

func TestResolveAgent_MultipleAmbiguous(t *testing.T) {
	in := spec.AgentResolveInput{Agents: map[string]json.RawMessage{
		"a": agentBody(t, spec.Agent{Command: []string{"a"}}),
		"b": agentBody(t, spec.Agent{Command: []string{"b"}}),
	}}
	_, err := resolveAgent(in)
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "--agent NAME") {
		t.Errorf("error should suggest --agent; got: %s", err)
	}
}

func TestResolveAgent_NotFound(t *testing.T) {
	in := spec.AgentResolveInput{
		Agents: map[string]json.RawMessage{"claude": agentBody(t, spec.Agent{Command: []string{"claude"}})},
		Name:   "missing",
	}
	if _, err := resolveAgent(in); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestSortedNames(t *testing.T) {
	catalog := map[string]spec.Agent{"zebra": {}, "alpha": {}, "mike": {}}
	got := sortedNames(catalog)
	want := []string{"alpha", "mike", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %v", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d] got %q, want %q", i, got[i], w)
		}
	}
}
