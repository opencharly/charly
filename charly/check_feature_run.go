package main

// check_feature_run.go — the `charly box feature run <image>` Agent Driven
// Evaluation (ADE) acceptance runner (the core box-grammar CLI leaf) + the shared
// step-reporting / grader-resolution helpers.
//
// The DEPLOY-scope `charly check feature run <deployment>` CLI leaf moved to the
// compiled-in command:check plugin (candy/plugin-check); its CLI-free engine
// (hostFeatureLive, wiring the host-side agent grader) lives behind the check-run
// seam (host_build_check_run.go). This file keeps the BUILD-scope `charly box
// feature run` leaf — which stays in the box grammar (image.go) — plus the helpers
// both the box leaf and the deploy-scope engine share: reportSteps / stepFailCount
// (step formatting), resolveGraderAgent (the kind:agent catalog resolution the
// grader needs), and validateTagExpr (the --tag syntax check).
//
// P12a follow-up ATTEMPTED moving BoxFeatureCmd/BoxFeatureRunCmd to candy/plugin-box
// as a 7th `box`-nested command word (CommandParent()=="box") — mirroring
// generate/validate/new/pkg/inspect/list — and REVERTED it: nesting a second
// "feature" word under `box` panics RegisterBuiltinPluginUnit at process init,
// because the provider registry's uniqueness key is provKey(class, word) alone
// (provider_registry.go), with NO CommandParent component, and candy/plugin-feature
// already owns the TOP-LEVEL {command, feature} word (`charly feature
// list/pending/validate`). This leaf stays in core until P12b resolves the
// collision (rename the nested word — breaks CLI parity — or make the registry
// key CommandParent-aware, a cross-cutting core change).
//
//   - `charly box feature run <image>` — BUILD scope. Disposable container
//     (podman run --rm per check); deterministic steps only. A prose-only step
//     has no stable target to probe, so it stays advisory-skip — use the
//     deploy-scope verb to agent-grade it.

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// ---------------------------------------------------------------------------
// Shared reporting
// ---------------------------------------------------------------------------

// stepFailCount returns how many steps ended in a fail verdict.
func stepFailCount(results []kit.StepResult) int {
	n := 0
	for _, r := range results {
		if r.Result.Status == TestFail {
			n++
		}
	}
	return n
}

// reportSteps writes results in the requested format and returns the fail
// count. Reuses the FormatStepResults* reporters (kit.FormatStepResults*, referenced directly).
func reportSteps(w io.Writer, results []kit.StepResult, format string) int {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		_ = kit.FormatStepResultsJSON(w, results)
	case "tap":
		kit.FormatStepResultsTAP(w, results)
	case "junit":
		_ = kit.FormatStepResultsJUnit(w, results)
	default:
		kit.FormatStepResultsText(w, results)
	}
	return stepFailCount(results)
}

// resolveGraderAgent loads the project's `agent:` catalog and resolves the named
// AI (or the sole entry when name is empty). Errors clearly when no AI is
// configured so the operator knows to add one or pass --no-agent.
func resolveGraderAgent(dir, name string) (*spec.AgentExecSpec, error) {
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading project for the ai: catalog: %w", err)
	}
	bodies := uf.PluginKinds["agent"]
	if !ok || uf == nil || len(bodies) == 0 {
		return nil, fmt.Errorf("agent grader needs a kind:agent entry (an `agent:` map in check.yml); add one or pass --no-agent for deterministic-only")
	}
	ai, _, err := resolveAgentViaPlugin(bodies, name)
	if err != nil {
		return nil, err
	}
	return ai, nil
}

// validateTagExpr syntax-checks an optional --tag expression (rejecting a malformed
// one). It does NOT apply the parsed expression as a step filter: kit.RunPlan (the
// walk both hostFeatureBox and hostFeatureLive drive, since the P12a architecture
// fold) takes no tag-filter parameter and walks every step unconditionally — a
// confirmed, RCA'd, non-blocking gap (per-tag filtering was never wired past this
// parse), routed to the next check-correctness thematic batch. Named + shaped for
// what it actually does now (was planTagFilter, whose *TagExpr return had no live
// caller once every RunPlan call site dropped the filter arg — R1/unparam).
func validateTagExpr(tag string) error {
	if strings.TrimSpace(tag) == "" {
		return nil
	}
	_, err := kit.ParseTagExpr(tag)
	return err
}

// ---------------------------------------------------------------------------
// charly box feature run <image>  (BUILD scope)
// ---------------------------------------------------------------------------

// BoxFeatureCmd groups `charly box feature run` (and room for future build-scope
// feature verbs). The run-verb lives here — a child of box/check, NOT part of the
// externalized inspection family (candy/plugin-feature) — so it fits the existing
// build-mode command hierarchy.
type BoxFeatureCmd struct {
	Run BoxFeatureRunCmd `cmd:"" help:"Run a box's baked plan steps against a disposable container (build scope; prose-only steps need a live deployment — see charly check feature run)"`
}

// BoxFeatureRunCmd: `charly box feature run <image>`. Build-scope acceptance —
// the image's baked plan steps run against a disposable container. Image refs
// resolve against local container storage (never charly.yml), same as
// `charly check box`.
type BoxFeatureRunCmd struct {
	Image  string `arg:"" help:"Image reference (full ref or short name resolved against local container storage)"`
	Format string `long:"format" default:"text" help:"Output format: text, json, tap, junit"`
	Tag    string `long:"tag" help:"Only run steps matching this tag expression (e.g. 'smoke and not slow')"`
	Strict bool   `long:"strict" help:"Treat prose-only (unbound) steps as failures instead of skips"`
}

func (c *BoxFeatureRunCmd) Run() error {
	// Transitional CLI shell over the CLI-free engine (hostFeatureBox), the same one the
	// "feature-box" atom arm calls — engine is the single source, ZERO behavior change.
	reply, err := hostFeatureBox(spec.CheckRunRequest{Mode: "feature-box", Image: c.Image, Tag: c.Tag, Strict: c.Strict})
	if err != nil {
		return err
	}
	if reply.NoSteps {
		fmt.Fprintln(os.Stderr, "No plan steps baked into this image (author a plan: with check: steps).")
		return nil
	}
	fmt.Fprintln(os.Stderr, reply.Header)
	fails := reportSteps(os.Stdout, reply.Steps, c.Format)
	if fails > 0 {
		return &sdk.ExitCodeError{Code: sdk.CheckFailExitCode, Err: fmt.Errorf("%d check(s) failed", fails)}
	}
	return nil
}
