package main

import (
	"encoding/json"
	"github.com/opencharly/spec/spec"
	"os"
	"path/filepath"
	"testing"
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
	if err := os.WriteFile(filepath.Join(dir, spec.UnifiedFileName), []byte(doc), 0o644); err != nil {
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

	// (3) The live compiled-in provider dispatch (ResolveKind → Invoke(OpResolve),
	// applying defaults like prompt_via → argv) — formerly proven here via a
	// core-side catalog resolver — is now exercised ONLY plugin-side:
	// candy/plugin-check/agent.go's resolveAgentSpec reaches the SAME
	// kind/"agent"/OpResolve dispatch via Executor.InvokeProvider, which needs
	// a live reverse-channel Executor a unit test cannot construct in isolation —
	// proven instead by any live `charly check feature run` bed carrying an
	// `agent:` catalog + grader (R10, not a core unit test).
}

// TestValidateIterateBed_RejectsUnknownAgent relocated to
// candy/plugin-loader/plugin_agent_module_test.go (#55 decoupling cone, Batch
// C) — it asserted loaderkit.ValidateIterateBed directly, zero charly
// coupling.
