package main

// agent_config.go — the HOST side of the `agent` kind (the AI-CLI grader catalog)
// after the agent de-type (Cutover E). The name-selection + default application
// (the former ResolveAgent) moved into candy/plugin-agent's OpResolve leg; the
// kernel stores agent bodies OPAQUELY and the check/iterate harness consumes a
// generic spec.AgentExecSpec (a resolved "launch + version-capture + iterate-poll
// a CLI" descriptor) — it never imports the concrete `agent` kind.
//
// The `charly check list-agent` table printer + the harness's version-capture helpers
// (VersionResult/ParseAgentTimeout) live in the compiled-in command:check plugin
// (candy/plugin-check/agent.go) with the rest of the `charly check` CLI + AI-harness.
// The host keeps only resolveAgentViaPlugin — the grader-catalog resolution the
// `charly box feature run` grader path needs.

import (
	"encoding/json"

	"github.com/opencharly/sdk/spec"
)

// ---------------------------------------------------------------------------
// Resolution (via candy/plugin-agent's OpResolve)
// ---------------------------------------------------------------------------

// resolveAgentViaPlugin selects + resolves the named agent (or the sole entry when
// name == "") from the OPAQUE catalog via candy/plugin-agent's OpResolve leg,
// returning a generic AgentExecSpec + the chosen name. The plugin owns the
// name-selection, the no-agents/not-found/multiple errors, and the default
// application; the kernel reads no spec.Agent fields.
func resolveAgentViaPlugin(bodies map[string]json.RawMessage, name string) (*spec.AgentExecSpec, string, error) {
	reply, err := hostInvoke[spec.AgentResolveInput, spec.AgentResolveReply](ClassKind, "agent", OpResolve, spec.AgentResolveInput{Agents: bodies, Name: name})
	if err != nil {
		return nil, "", err
	}
	return reply.Spec, reply.Name, nil
}
