package agentkind

// resolve.go — candy/plugin-agent's OpResolve leg (the agent de-type, Cutover E).
// The name-selection + Go-level default application the kernel used to do inline
// (the former ResolveAgent) lives HERE now: the host hands the OPAQUE agent
// catalog + a selected name, and this returns a generic AgentExecSpec the kernel's
// check/iterate harness runs WITHOUT importing the concrete `agent` kind.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// defaultAgentTimeout is the resolved Timeout when an entry leaves it empty —
// "" means no per-iteration wall-clock cap (the loop is plateau-bounded).
const defaultAgentTimeout = ""

// resolveAgent selects the named agent (or the sole entry when name == "") from
// the opaque catalog, applies defaults, and returns its AgentExecSpec.
func resolveAgent(in spec.AgentResolveInput) (spec.AgentResolveReply, error) {
	if len(in.Agents) == 0 {
		return spec.AgentResolveReply{}, fmt.Errorf("check: no agents configured (add an 'agent:' map to check.yml)")
	}
	catalog := make(map[string]spec.Agent, len(in.Agents))
	for name, body := range in.Agents {
		var a spec.Agent
		if err := json.Unmarshal(body, &a); err != nil {
			return spec.AgentResolveReply{}, fmt.Errorf("agent %q: %w", name, err)
		}
		catalog[name] = a
	}

	name := in.Name
	if name == "" {
		if len(catalog) > 1 {
			return spec.AgentResolveReply{}, fmt.Errorf("harness: multiple agents configured (%s); pass --agent NAME to pick one",
				strings.Join(sortedNames(catalog), ", "))
		}
		for k := range catalog {
			name = k
		}
	}
	a, ok := catalog[name]
	if !ok {
		return spec.AgentResolveReply{}, fmt.Errorf("harness: ai not found: %q (available: %s)",
			name, strings.Join(sortedNames(catalog), ", "))
	}

	// Apply Go-level defaults (the former ResolveAgent.apply).
	timeout := a.Timeout
	if timeout == "" {
		timeout = defaultAgentTimeout
	}
	promptVia := a.PromptVia
	if promptVia == "" {
		promptVia = "argv"
	}
	return spec.AgentResolveReply{
		Name: name,
		Spec: &spec.AgentExecSpec{
			Command:                      a.Command,
			PromptVia:                    promptVia,
			VersionCommand:               a.VersionCommand,
			Timeout:                      timeout,
			Env:                          a.Env,
			WorkingDir:                   a.WorkingDir,
			Credential:                   a.Credential,
			ProgressCheckInterval:        a.ProgressCheckInterval,
			ProgressNoImprovementTimeout: a.ProgressNoImprovementTimeout,
			OutputFormat:                 a.OutputFormat,
		},
	}, nil
}

func sortedNames(catalog map[string]spec.Agent) []string {
	out := make([]string, 0, len(catalog))
	for k := range catalog {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
