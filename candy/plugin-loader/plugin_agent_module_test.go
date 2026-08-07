package loader

// Relocated from charly/plugin_agent_module_test.go (#55 decoupling cone, Batch
// C): TestValidateIterateBed_RejectsUnknownAgent asserts loaderkit.ValidateIterateBed
// directly against a spec.UnifiedFile fixture — zero charly coupling.
// TestLoadUnified_AgentPluginKind (the LoadUnified white-box round-trip) STAYS in
// charly/plugin_agent_module_test.go.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// TestValidateIterateBed_RejectsUnknownAgent proves the LOAD-BEARING guard: an
// iterate bed that references an agent NOT in the catalog is rejected — the
// catalog is read via uf.Agents() (the name-keyed accessor over
// uf.PluginKinds). A known agent passes the guard.
func TestValidateIterateBed_RejectsUnknownAgent(t *testing.T) {
	// A catalog (a plugin kind) containing exactly "claude".
	uf := &spec.UnifiedFile{PluginKinds: map[string]map[string]json.RawMessage{
		"agent": {"claude": json.RawMessage(`{"command":["claude"]}`)},
	}}

	good := &spec.FleetNode{
		Iterate: &spec.Iterate{Agent: []string{"claude"}, Sandbox: "check-sandbox"},
		Plan:    []spec.Step{{Check: "the service responds"}},
	}
	if err := loaderkit.ValidateIterateBed(uf, "bed", good); err != nil {
		t.Fatalf("known agent 'claude' was rejected: %v", err)
	}

	bad := &spec.FleetNode{
		Iterate: &spec.Iterate{Agent: []string{"ghost"}, Sandbox: "check-sandbox"},
		Plan:    []spec.Step{{Check: "the service responds"}},
	}
	err := loaderkit.ValidateIterateBed(uf, "bed", bad)
	if err == nil || !strings.Contains(err.Error(), "is not defined in the agent: catalog") {
		t.Fatalf("unknown agent 'ghost' was NOT rejected by the catalog guard, got err=%v", err)
	}
}
