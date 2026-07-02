package main

// agent_config.go — `kind: agent` entity (the reusable AI-CLI catalog).
//
// One entry per AI CLI (claude, codex, gemini, ...). Each entry is the
// *invocation contract*: how to launch the CLI, how to authenticate it,
// how to capture its version, and how long to let it run.
//
// An `iterate:` block (on a deploy / kind:check bed) references AIs by name and
// inherits nothing from them — the iterate block carries the prompt + plateau
// policy, AIs carry the binary contract. A new AI is added once and reused by
// every iterate block; a new benchmark doesn't need to redeclare its AIs.

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
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// AgentOutputFormatStreamJSON is the explicit non-default value of
// AgentConfig.OutputFormat (the default "" is the plain format). The legal set
// {"", "stream-json"} is enforced by the closed CUE schema at load
// (agent.cue: output_format: *"" | "stream-json").
const AgentOutputFormatStreamJSON = "stream-json"

// DefaultProgressCheckInterval / DefaultProgressNoImprovementTimeout are
// the Go-level defaults the harness loop applies when an AI's
// progress_* fields are empty strings. Per the user spec for Round 3:
// poll every 5 minutes; terminate after 30 minutes of no scoring
// improvement. Both configurable per-AI via the yaml fields above.
const (
	DefaultProgressCheckInterval        = 5 * time.Minute
	DefaultProgressNoImprovementTimeout = 30 * time.Minute
)

// DefaultAgentTimeout is the Go-level default applied by ResolveAgent when an
// AI entry's `timeout:` field is empty. Empty string = no per-iteration
// timeout (the harness loop is plateau-bounded, not wall-clock-bounded;
// the score's prompt promises "Take all the time you need" and the
// runner must honor that). Authors who DO want a wall-clock cap can
// set `ai.<name>.timeout: 30m` (or any Go duration) on their AI entry.
const DefaultAgentTimeout = ""

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

var (
	// ErrNoAgents fires when the project has no `agent:` map (no agents configured).
	ErrNoAgents = errors.New("check: no agents configured (add an 'agent:' map to check.yml)")

	// ErrAgentNotFound fires when the requested AI name is absent from the catalog.
	ErrAgentNotFound = errors.New("harness: ai not found")
)

// ---------------------------------------------------------------------------
// Resolution + defaults
// ---------------------------------------------------------------------------

// Agents reconstructs the name-keyed AI-CLI grader catalog from uf.PluginKinds.
// The `agent` kind is a plugin kind (candy/plugin-agent) — an `agent:` node lands in
// uf.PluginKinds["agent"][<name>] as canonical spec.Agent JSON (produced by the
// plugin's Invoke). This accessor decodes each body back into *AgentConfig
// (= *spec.Agent), yielding the SAME map[string]*AgentConfig shape the harness
// consumed when agent was a typed core map (the former uf.Agent). Recomputed per call
// (the catalog is a handful of CLIs); returns nil when no agents are configured. A
// decode error is impossible in practice — the body is canonical JSON the plugin
// Marshalled from spec.Agent — but a bad entry is skipped rather than poisoning the
// whole catalog.
func (uf *UnifiedFile) Agents() map[string]*AgentConfig {
	if uf == nil {
		return nil
	}
	bodies := uf.PluginKinds["agent"]
	if len(bodies) == 0 {
		return nil
	}
	out := make(map[string]*AgentConfig, len(bodies))
	for name, body := range bodies {
		var a AgentConfig
		if err := json.Unmarshal(body, &a); err != nil {
			continue
		}
		out[name] = &a
	}
	return out
}

// ResolveAgent returns the named AI entry, or the sole entry if name == "" and
// exactly one is configured. Applies Go-level defaults:
//   - Timeout: "" (no per-iteration wall-clock cap) when empty
//   - PromptVia: "argv" when empty
//
// Returns a *copy* so callers can mutate without poisoning the catalog.
func ResolveAgent(catalog map[string]*AgentConfig, name string) (*AgentConfig, string, error) {
	if len(catalog) == 0 {
		return nil, "", ErrNoAgents
	}

	apply := func(ai AgentConfig) *AgentConfig {
		out := ai
		if out.Timeout == "" {
			out.Timeout = DefaultAgentTimeout
		}
		if out.PromptVia == "" {
			out.PromptVia = "argv"
		}
		return &out
	}

	if name == "" {
		if len(catalog) > 1 {
			return nil, "", fmt.Errorf("harness: multiple agents configured (%s); pass --agent NAME to pick one",
				strings.Join(SortedAgentNames(catalog), ", "))
		}
		// Exactly one entry — pick it.
		for k, v := range catalog {
			return apply(*v), k, nil
		}
	}

	ai, ok := catalog[name]
	if !ok {
		return nil, "", fmt.Errorf("%w: %q (available: %s)",
			ErrAgentNotFound, name, strings.Join(SortedAgentNames(catalog), ", "))
	}
	return apply(*ai), name, nil
}

// SortedAgentNames returns AI names in alphabetical order. Useful for
// deterministic error messages and stable test expectations.
func SortedAgentNames(catalog map[string]*AgentConfig) []string {
	out := make([]string, 0, len(catalog))
	for k := range catalog {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ParseAgentTimeout parses the AI's Timeout field. Empty string (the
// default) returns 0 — meaning "no wall-clock cap on the runner".
// Callers in harness_loop should branch on `dur == 0` to skip
// `context.WithTimeout` entirely (use plain `context.WithCancel`)
// so the runner inherits the parent context's cancellation only.
// Plateau detection is the bound, not wall clock.
//
// Authors who DO want a per-iteration cap (e.g., for cost control on
// shared infrastructure) can set `ai.<name>.timeout: 30m` (or any
// Go duration) on their AI entry; that path still uses the timeout.
func ParseAgentTimeout(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

// ---------------------------------------------------------------------------
// Version capture
// ---------------------------------------------------------------------------

// VersionResult is the captured outcome of one `version_command:` run.
// On success, Stdout is the trimmed first line of stdout. On failure,
// Stdout is empty and Error is non-empty (e.g. "exit status 127: command not found").
type VersionResult struct {
	Stdout string `yaml:"stdout,omitempty" json:"stdout,omitempty"`
	Error  string `yaml:"error,omitempty"  json:"error,omitempty"`
}

// String renders the version for the result file's agent_version: block.
// Successful captures show the trimmed stdout; failures show "error: <...>".
func (v VersionResult) String() string {
	if v.Error != "" {
		return "error: " + v.Error
	}
	return v.Stdout
}

// CaptureVersion runs the AI's `version_command:` via the supplied
// executor's Run callback (LocalExecutor / NestedExecutor / SSHExecutor).
// Returns a VersionResult capturing trimmed stdout or the error string.
//
// Failure of the version command is NOT fatal to a harness run — the
// loop carries on and records the failure in result.<calver>.yml under
// `agent_version:` so future readers can see what broke.
//
// run is `func(ctx, argv []string) (stdout, stderr string, err error)`.
// The harness loop adapts each executor to this signature so AI version
// capture is independent of the executor implementation.
func CaptureVersion(
	ctx context.Context,
	ai *AgentConfig,
	run func(ctx context.Context, argv []string) (string, string, error),
) VersionResult {
	if len(ai.VersionCommand) == 0 {
		return VersionResult{Error: "version_command: not configured"}
	}
	stdout, stderr, err := run(ctx, ai.VersionCommand)
	if err != nil {
		// Surface stderr in the error message when present; helps users
		// debug "claude --version" failures (e.g., binary not on PATH).
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

// LocalCaptureVersion is a convenience wrapper that runs the version
// command on the host directly (for a host-target iterate sandbox). Exposed so
// the host-target preflight + the per-AI capture share one path.
func LocalCaptureVersion(ctx context.Context, ai *AgentConfig) VersionResult {
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

// firstNonEmptyLine returns the first non-empty line of s with surrounding
// whitespace trimmed. Used to normalize multi-line --version output
// (some CLIs print "Foo CLI" + version on separate lines).
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

// PrintAgents writes a human-readable table of configured agents to w.
// Used by `charly check list-ai`.
func PrintAgents(w io.Writer, catalog map[string]*AgentConfig) {
	if len(catalog) == 0 {
		fmt.Fprintln(w, "No agents configured. Add an 'agent:' map to check.yml.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCOMMAND\tVERSION_COMMAND\tTIMEOUT\tPROMPT_VIA\tCREDENTIAL")
	for _, name := range SortedAgentNames(catalog) {
		ai := catalog[name]
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
