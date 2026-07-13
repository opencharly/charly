package check

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/spec"
)

// feature_cmd.go — the `charly check feature run` leaf (Agent Driven Evaluation, DEPLOY scope). It
// runs a running deployment's OWN baked plan as acceptance tests: deterministic check: steps run
// their probes and prose-only agent-check: steps bind to the agent grader (unless --no-agent). All
// the gathering AND the agent-grader wiring run HOST-side behind the "check-run" seam
// (Mode:"feature-live", which threads Tag/Agent/Timeout/Strict/NoAgent); the plugin sends the CLI
// inputs, prints the host-built Header banner, and formats the returned []StepResult.
//
// The build-scope `charly box feature run <image>` leaf stays under the `box` command tree in core
// (it feeds the same host engine over a feature-box seam mode) — this file owns ONLY the
// check-side, deploy-scope `charly check feature run` leaf.

// CheckFeatureCmd groups `charly check feature run` under the live-check hierarchy.
type CheckFeatureCmd struct {
	Run CheckFeatureRunCmd `cmd:"" help:"Run a running deployment's baked plan steps as acceptance tests; prose-only steps are agent-graded (Agent Driven Evaluation)"`
}

// CheckFeatureRunCmd: `charly check feature run <deployment>`. Deploy-scope acceptance against a
// running image-backed (pod) deployment. Deterministic steps run their embedded check; prose-only
// steps bind to the agent grader (unless --no-agent), which probes the live deployment for
// evidence. Mirrors the former in-core CheckFeatureRunCmd flags + help.
type CheckFeatureRunCmd struct {
	Box      string `arg:"" help:"Deployment name (a box-backed pod deployment)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Format   string `long:"format" default:"text" help:"Output format: text, json, tap, junit"`
	Tag      string `long:"tag" help:"Only run steps matching this tag expression"`
	Agent    string `long:"agent" help:"kind:agent entry to use as the prose-step grader (default: the sole configured agent)"`
	Timeout  string `long:"timeout" help:"Per-grader-call wall-clock cap (Go duration; default 5m or the ai entry's timeout)"`
	NoAgent  bool   `long:"no-agent" help:"Deterministic-only: do not agent-grade prose-only steps (they report as unbound/skip)"`
	Strict   bool   `long:"strict" help:"Treat unbound steps as failures (only meaningful with --no-agent)"`
}

func (c *CheckFeatureRunCmd) Run() error {
	reply, err := hostCheckRun(spec.CheckRunRequest{
		Mode:     "feature-live",
		Name:     c.Box,
		Instance: c.Instance,
		Tag:      c.Tag,
		Agent:    c.Agent,
		Timeout:  c.Timeout,
		Strict:   c.Strict,
		NoAgent:  c.NoAgent,
	})
	if err != nil {
		return err
	}
	if reply.NoSteps {
		fmt.Fprintln(os.Stderr, "No plan steps baked into this deployment's image (author a plan: with check: steps).")
		return nil
	}
	if reply.Header != "" {
		fmt.Fprintln(os.Stderr, reply.Header)
	}
	// Feature run reports to stdout (the former in-core CheckFeatureRunCmd wrote reportSteps to
	// os.Stdout — box/live write to os.Stderr; preserved here for behaviour-neutrality).
	fails := reportSteps(os.Stdout, reply.Steps, c.Format)
	if fails > 0 {
		return &CheckFailedError{Failed: fails}
	}
	return nil
}
