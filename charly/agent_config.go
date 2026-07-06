package main

// agent_config.go — the HOST side of the `agent` kind (the AI-CLI grader catalog)
// after the agent de-type (Cutover E). The name-selection + default application
// (the former ResolveAgent) moved into candy/plugin-agent's OpResolve leg; the
// kernel stores agent bodies OPAQUELY and the check/iterate harness consumes a
// generic spec.AgentExecSpec (a resolved "launch + version-capture + iterate-poll
// a CLI" descriptor) — it never imports the concrete `agent` kind.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/opencharly/sdk/spec"
)

// AgentOutputFormatStreamJSON is the explicit non-default value of
// AgentExecSpec.OutputFormat (the default "" is the plain format). The legal set
// {"", "stream-json"} is enforced by the closed CUE schema at load
// (agent.cue: output_format: *"" | "stream-json").
const AgentOutputFormatStreamJSON = "stream-json"

// DefaultProgressCheckInterval / DefaultProgressNoImprovementTimeout are the
// Go-level defaults the harness loop applies when a resolved AI's progress_*
// fields are empty: poll every 5 minutes; terminate after 30 minutes of no
// scoring improvement. Both configurable per-AI via the yaml fields.
const (
	DefaultProgressCheckInterval        = 5 * time.Minute
	DefaultProgressNoImprovementTimeout = 30 * time.Minute
)

// DefaultAgentTimeout is the resolved Timeout when an AI entry leaves `timeout:`
// empty — "" means no per-iteration wall-clock cap (the loop is plateau-bounded).
// Applied by candy/plugin-agent's OpResolve; named here only for the list display.
const DefaultAgentTimeout = ""

// ---------------------------------------------------------------------------
// Resolution (via candy/plugin-agent's OpResolve)
// ---------------------------------------------------------------------------

// resolveAgentViaPlugin selects + resolves the named agent (or the sole entry when
// name == "") from the OPAQUE catalog via candy/plugin-agent's OpResolve leg,
// returning a generic AgentExecSpec + the chosen name. The plugin owns the
// name-selection, the no-agents/not-found/multiple errors, and the default
// application; the kernel reads no spec.Agent fields.
func resolveAgentViaPlugin(bodies map[string]json.RawMessage, name string) (*spec.AgentExecSpec, string, error) {
	prov, ok := providerRegistry.ResolveKind("agent")
	if !ok {
		return nil, "", fmt.Errorf("agent resolve: kind provider not registered")
	}
	paramsJSON, err := json.Marshal(spec.AgentResolveInput{Agents: bodies, Name: name})
	if err != nil {
		return nil, "", fmt.Errorf("agent resolve: marshal input: %w", err)
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: "agent", Op: OpResolve, Params: json.RawMessage(paramsJSON)})
	if err != nil {
		return nil, "", err
	}
	var reply spec.AgentResolveReply
	if out != nil && len(out.JSON) > 0 {
		if err := json.Unmarshal(out.JSON, &reply); err != nil {
			return nil, "", fmt.Errorf("agent resolve: decode reply: %w", err)
		}
	}
	return reply.Spec, reply.Name, nil
}

// ---------------------------------------------------------------------------
// Version capture
// ---------------------------------------------------------------------------

// VersionResult is the captured outcome of one `version_command:` run. On success,
// Stdout is the trimmed first line of stdout. On failure, Stdout is empty and Error
// is non-empty (e.g. "exit status 127: command not found").
type VersionResult struct {
	Stdout string `yaml:"stdout,omitempty" json:"stdout,omitempty"`
	Error  string `yaml:"error,omitempty"  json:"error,omitempty"`
}

// String renders the version for the result file's agent_version: block.
func (v VersionResult) String() string {
	if v.Error != "" {
		return "error: " + v.Error
	}
	return v.Stdout
}

// CaptureVersion runs the resolved AI's `version_command:` via the supplied
// executor's Run callback (LocalExecutor / NestedExecutor / SSHExecutor). Returns
// a VersionResult capturing trimmed stdout or the error string. Failure is NOT
// fatal — the loop carries on and records it under agent_version:.
func CaptureVersion(
	ctx context.Context,
	ai *spec.AgentExecSpec,
	run func(ctx context.Context, argv []string) (string, string, error),
) VersionResult {
	if len(ai.VersionCommand) == 0 {
		return VersionResult{Error: "version_command: not configured"}
	}
	stdout, stderr, err := run(ctx, ai.VersionCommand)
	if err != nil {
		msg := err.Error()
		if s := strings.TrimSpace(stderr); s != "" {
			msg = msg + ": " + s
		}
		return VersionResult{Error: msg}
	}
	first := firstNonEmptyLine(stdout)
	if first == "" {
		return VersionResult{Error: "version_command: produced empty output"}
	}
	return VersionResult{Stdout: first}
}

// LocalCaptureVersion runs the version command on the host directly (for a
// host-target iterate sandbox). Exposed so the host-target preflight + the per-AI
// capture share one path.
func LocalCaptureVersion(ctx context.Context, ai *spec.AgentExecSpec) VersionResult {
	return CaptureVersion(ctx, ai, func(ctx context.Context, argv []string) (string, string, error) {
		if len(argv) == 0 {
			return "", "", errors.New("argv empty")
		}
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	})
}

// ParseAgentTimeout parses a resolved Duration field (Timeout / progress_*). Empty
// (the default) returns 0 — "no wall-clock cap"; callers branch on `dur == 0` to
// skip context.WithTimeout entirely so the plateau bound governs.
func ParseAgentTimeout(s spec.Duration) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(string(s))
}

// firstNonEmptyLine returns the first non-empty line of s with surrounding
// whitespace trimmed. Used to normalize multi-line --version output.
func firstNonEmptyLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Listing
// ---------------------------------------------------------------------------

// PrintAgents writes a human-readable table of configured agents to w (for
// `charly check list-ai`). It peeks the display fields from each OPAQUE body — the
// kernel does not type agent bodies (the agent de-type, Cutover E).
func PrintAgents(w io.Writer, catalog map[string]json.RawMessage) {
	if len(catalog) == 0 {
		fmt.Fprintln(w, "No agents configured. Add an 'agent:' map to check.yml.")
		return
	}
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCOMMAND\tVERSION_COMMAND\tTIMEOUT\tPROMPT_VIA\tCREDENTIAL")
	for _, name := range names {
		var ai struct {
			Command        []string          `json:"command"`
			PromptVia      string            `json:"prompt_via"`
			VersionCommand []string          `json:"version_command"`
			Timeout        string            `json:"timeout"`
			Credential     []json.RawMessage `json:"credential"`
		}
		_ = json.Unmarshal(catalog[name], &ai)
		timeout := ai.Timeout
		if timeout == "" {
			timeout = DefaultAgentTimeout + " (default)"
		}
		promptVia := ai.PromptVia
		if promptVia == "" {
			promptVia = "argv (default)"
		}
		cmd := strings.Join(ai.Command, " ")
		if len(cmd) > 50 {
			cmd = cmd[:47] + "..."
		}
		ver := strings.Join(ai.VersionCommand, " ")
		if ver == "" {
			ver = "(none)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\n",
			name, cmd, ver, timeout, promptVia, len(ai.Credential))
	}
	_ = tw.Flush()
}
