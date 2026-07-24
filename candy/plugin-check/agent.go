package check

// agent.go — the harness's AGENT-spec access (P12; the check-run harness's OWN grader wiring,
// K1-unblock wave arm 2, joined this file's callers with feature_run_gather.go). The plugin needs
// (a) to resolve an opaque agent body → a generic spec.AgentExecSpec and (b) the pure
// launch/version/timeout helpers that operate on that resolved spec.
//
// (a) is candy/plugin-agent's OpResolve leg (the agent de-type, Cutover E): this plugin gets the
// opaque kind:agent catalog from the resolved-project envelope (AgentBodies) and dispatches it
// back through the host registry to InvokeProvider(kind:agent, OpResolve) directly — the ONE
// resolve call every consumer here (synccreds.go, runlocal.go, feature_run_gather.go's
// pluginCheckRunFeatureLive grader) shares (R3), so the name-selection + default application is
// single-sourced in the agent plugin, NOT re-implemented here. (b) are pure Go helpers over the
// resolved spec.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// AgentOutputFormatStreamJSON is the explicit non-default value of
// AgentExecSpec.OutputFormat (the default "" is the plain format).
const AgentOutputFormatStreamJSON = "stream-json"

// DefaultProgressCheckInterval / DefaultProgressNoImprovementTimeout are the
// Go-level defaults the harness loop applies when a resolved AI's progress_* fields
// are empty: poll every 5 minutes; terminate after 30 minutes of no scoring
// improvement.
const (
	DefaultProgressCheckInterval        = 5 * time.Minute
	DefaultProgressNoImprovementTimeout = 30 * time.Minute
)

// DefaultAgentTimeout is the resolved Timeout when an AI entry leaves `timeout:`
// empty — "" means no per-iteration wall-clock cap (the loop is plateau-bounded).
const DefaultAgentTimeout = ""

// resolveAgentSpec selects + resolves the named agent (or the sole entry when name
// == "") from the OPAQUE catalog via candy/plugin-agent's OpResolve leg over the
// host registry (InvokeProvider(kind:agent, OpResolve)). Returns the generic AgentExecSpec.
func resolveAgentSpec(ex *sdk.Executor, ctx context.Context, bodies map[string]json.RawMessage, name string) (*spec.AgentExecSpec, error) {
	if ex == nil {
		return nil, fmt.Errorf("charly check: agent resolution requires compiled-in placement (the reverse channel is unavailable out-of-process)")
	}
	inJSON, err := json.Marshal(spec.AgentResolveInput{Agents: bodies, Name: name})
	if err != nil {
		return nil, err
	}
	out, err := ex.InvokeProvider(ctx, "kind", "agent", sdk.OpResolve, inJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return nil, err
	}
	var reply spec.AgentResolveReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return nil, fmt.Errorf("resolve agent %q: decode reply: %w", name, err)
	}
	if reply.Spec == nil {
		return nil, fmt.Errorf("resolve agent %q: empty exec spec", name)
	}
	return reply.Spec, nil
}

// VersionResult is the captured outcome of one `version_command:` run. On success,
// Stdout is the trimmed first line of stdout. On failure, Stdout is empty and Error
// is non-empty.
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

// CaptureVersion runs the resolved AI's `version_command:` via the supplied run
// callback. Returns a VersionResult capturing trimmed stdout or the error string.
// Failure is NOT fatal — the loop carries on and records it under agent_version:.
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
// host-target iterate sandbox).
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
	return time.ParseDuration(s)
}

// firstNonEmptyLine returns the first non-empty line of s with surrounding
// whitespace trimmed.
func firstNonEmptyLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
