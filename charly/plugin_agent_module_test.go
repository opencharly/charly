package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestLoadUnified_AgentPluginKind proves the agent kind→plugin extraction end-to-end
// through the REAL loader: the AI-CLI grader catalog (formerly the typed core map
// uf.Agent) now lands in uf.PluginKinds["agent"], NAME-KEYED, and the Agents()
// accessor reconstructs the same map[string]*AgentConfig the harness consumes. The
// authored form (`agent:`) is UNCHANGED — these nodes mirror the root charly.yml
// catalog (claude / codex), validated at load against the plugin's served #AgentInput.
func TestLoadUnified_AgentPluginKind(t *testing.T) {
	dir := t.TempDir()
	doc := `version: "` + latestSchemaVersion.String() + `"
claude:
  agent:
    description: Anthropic Claude Code CLI
    command: [claude, -p, "${PROMPT}"]
    output_format: stream-json
    version_command: [claude, --version]
codex:
  agent:
    description: OpenAI Codex CLI
    command: [codex, exec, "${PROMPT}"]
`
	if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified agent plugin kind: %v", err)
	}

	// (1) The entities land in uf.PluginKinds["agent"], NAME-KEYED (not the former
	// typed uf.Agent core map).
	raw := uf.PluginKinds["agent"]
	if len(raw) != 2 {
		t.Fatalf("expected 2 agent entities in uf.PluginKinds, got %d (%v)", len(raw), raw)
	}
	if _, ok := raw["claude"]; !ok {
		t.Fatalf("agent entity not keyed by node name 'claude'; keys %v", raw)
	}

	// (2) The opaque body carries the authored fields (peek without typing — the
	// kernel does not decode agent bodies).
	var claude struct {
		Command      []string `json:"command"`
		OutputFormat string   `json:"output_format"`
	}
	if err := json.Unmarshal(raw["claude"], &claude); err != nil {
		t.Fatalf("decode claude body: %v", err)
	}
	if len(claude.Command) == 0 || claude.Command[0] != "claude" {
		t.Errorf("claude.Command = %v, want it to start with 'claude'", claude.Command)
	}
	if claude.OutputFormat != "stream-json" {
		t.Errorf("claude.OutputFormat = %q, want %q", claude.OutputFormat, "stream-json")
	}

	// (3) resolveAgentViaPlugin resolves a known name through the REAL compiled-in
	// provider (ResolveKind → Invoke(OpResolve)) — the live-dispatch seam the harness
	// uses, with the plugin applying defaults (prompt_via → argv).
	execSpec, name, err := resolveAgentViaPlugin(uf.PluginKinds["agent"], "claude")
	if err != nil {
		t.Fatalf("resolveAgentViaPlugin(claude): %v", err)
	}
	if name != "claude" || execSpec == nil || execSpec.PromptVia != "argv" {
		t.Fatalf("resolveAgentViaPlugin returned name=%q spec=%v, want claude with default prompt_via", name, execSpec)
	}
}

// TestValidateIterateBed_RejectsUnknownAgent proves the LOAD-BEARING guard survives
// the agent extraction: validateIterateBed reads the catalog via uf.Agents() (the
// name-keyed accessor over uf.PluginKinds), so an iterate bed that references an
// agent NOT in the catalog is still rejected — the behavior the pre-Cutover-A
// nameless append-list would have broken (it could not key by name). A known agent
// passes the guard.
func TestValidateIterateBed_RejectsUnknownAgent(t *testing.T) {
	// A catalog (now a plugin kind) containing exactly "claude".
	uf := &UnifiedFile{PluginKinds: map[string]map[string]json.RawMessage{
		"agent": {"claude": json.RawMessage(`{"command":["claude"]}`)},
	}}

	good := &BundleNode{
		Iterate: &spec.Iterate{Agent: []string{"claude"}, Sandbox: "check-sandbox"},
		Plan:    []spec.Step{{Check: "the service responds"}},
	}
	if err := validateIterateBed(uf, "bed", good); err != nil {
		t.Fatalf("known agent 'claude' was rejected: %v", err)
	}

	bad := &BundleNode{
		Iterate: &spec.Iterate{Agent: []string{"ghost"}, Sandbox: "check-sandbox"},
		Plan:    []spec.Step{{Check: "the service responds"}},
	}
	err := validateIterateBed(uf, "bed", bad)
	if err == nil || !strings.Contains(err.Error(), "is not defined in the agent: catalog") {
		t.Fatalf("unknown agent 'ghost' was NOT rejected by the catalog guard, got err=%v", err)
	}
}
