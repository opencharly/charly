package main

// check_feature_run.go — the Agent Driven Evaluation (ADE) acceptance
// runners: `charly box feature run <image>` and `charly check feature run <deployment>`.
//
// These run an entity's OWN baked plan steps (the `plan:` list, shipped in
// the ai.opencharly.description OCI label) as acceptance tests — the RUN
// half of the `charly feature {list,pending,validate}` family (the inspection
// half is the externalized command plugin candy/plugin-feature, which shells
// back to the hidden `charly __feature-{list,pending,validate}` core commands
// in feature_internal_cmd.go). Both reuse the shared plan engine
// (RunPlan, description_run.go) and the same target/var resolution as
// `charly check box` / `charly check live` (R3); the only new behaviour is surfacing
// step results as a first-class pass/fail run and, for the live verb,
// wiring the agent grader so prose-only steps are agent-graded.
//
//   - `charly box feature run <image>`     — BUILD scope. Disposable container
//     (podman run --rm per check); deterministic steps only. A prose-only
//     step has no stable target to probe, so it stays advisory-skip — use the
//     live verb to agent-grade it.
//   - `charly check feature run <name>`     — DEPLOY scope. Against a running
//     image-backed (pod) deployment; deterministic steps run their checks and
//     prose-only steps bind to the agent grader (unless --no-agent), which
//     probes the live deployment and returns a pass/fail verdict.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// Shared reporting
// ---------------------------------------------------------------------------

// stepFailCount returns how many steps ended in a fail verdict.
func stepFailCount(results []StepResult) int {
	n := 0
	for _, r := range results {
		if r.Result.Status == TestFail {
			n++
		}
	}
	return n
}

// reportSteps writes results in the requested format and returns the fail
// count. Reuses the FormatStepResults* reporters (description_report.go).
func reportSteps(w io.Writer, results []StepResult, format string) int {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		_ = FormatStepResultsJSON(w, results)
	case "tap":
		FormatStepResultsTAP(w, results)
	case "junit":
		_ = FormatStepResultsJUnit(w, results)
	default:
		FormatStepResultsText(w, results)
	}
	return stepFailCount(results)
}

// resolveGraderAgent loads the project's `agent:` catalog and resolves the named
// AI (or the sole entry when name is empty). Errors clearly when no AI is
// configured so the operator knows to add one or pass --no-agent.
func resolveGraderAgent(dir, name string) (*AgentConfig, error) {
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading project for the ai: catalog: %w", err)
	}
	agents := uf.Agents()
	if !ok || uf == nil || len(agents) == 0 {
		return nil, fmt.Errorf("agent grader needs a kind:agent entry (an `agent:` map in check.yml); add one or pass --no-agent for deterministic-only")
	}
	ai, _, err := ResolveAgent(agents, name)
	if err != nil {
		return nil, err
	}
	return ai, nil
}

// planTagFilter parses an optional --tag expression into a TagExpr.
func planTagFilter(tag string) (*TagExpr, error) {
	if strings.TrimSpace(tag) == "" {
		return nil, nil
	}
	return ParseTagExpr(tag)
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
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	imageRef, err := resolveLocalImageRef(rt.RunEngine, c.Image)
	if err != nil {
		return err
	}
	meta, err := ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		fmt.Fprintln(os.Stderr, "No plan steps baked into this image (author a plan: with check: steps).")
		return nil
	}
	filter, err := planTagFilter(c.Tag)
	if err != nil {
		return fmt.Errorf("parsing --tag: %w", err)
	}

	runner := NewRunner(ImageChain(rt.RunEngine, imageRef), ResolveCheckVarsBuild(meta), RunModeBox)
	runner.Distros = meta.Distro
	// ADE acceptance: verify the built image; do NOT re-run the build-time
	// install (run:) steps — they provisioned the image during the Containerfile
	// build and reference build-only context (/ctx) absent from the disposable
	// feature-run container.
	runner.SkipDeterministicRun = true
	// Build scope: no live target to probe, so no grader — prose-only steps
	// stay advisory (skip, or fail under --strict).
	results := RunPlan(context.Background(), runner, meta.Description, filter, c.Strict)

	fmt.Fprintf(os.Stderr, "Feature run (image, build scope): %s\n", imageRef)
	fails := reportSteps(os.Stdout, results, c.Format)
	if fails > 0 {
		return &CheckFailedError{Failed: fails}
	}
	return nil
}

// ---------------------------------------------------------------------------
// charly check feature run <deployment>  (DEPLOY scope)
// ---------------------------------------------------------------------------

// CheckFeatureCmd groups `charly check feature run` under the live-check hierarchy.
type CheckFeatureCmd struct {
	Run CheckFeatureRunCmd `cmd:"" help:"Run a running deployment's baked plan steps as acceptance tests; prose-only steps are agent-graded (Agent Driven Evaluation)"`
}

// CheckFeatureRunCmd: `charly check feature run <deployment>`. Deploy-scope
// acceptance against a running image-backed (pod) deployment. Deterministic
// steps run their embedded check; prose-only steps bind to the agent grader
// (unless --no-agent), which probes the live deployment for evidence.
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
	engine, containerName, err := resolveContainer(c.Box, c.Instance)
	if err != nil {
		return err
	}

	dir, _ := os.Getwd()
	var projectCfg *Config
	if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
		projectCfg = uf.ProjectConfig()
	}

	// Resolve the deploy key → declared image short-name via the shared
	// resolver, then to a registry ref, then read its OCI labels — the same
	// metadata path `charly check live` uses (R3).
	imageRef := resolveDeployBoxName(c.Box, c.Instance)
	resolvedRef, err := resolveImageRefForEnsure(imageRef, projectCfg, dir)
	if err != nil {
		return fmt.Errorf("resolving deploy box %q: %w", imageRef, err)
	}
	meta, err := ExtractMetadata(engine, resolvedRef)
	if err != nil {
		return err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		fmt.Fprintln(os.Stderr, "No plan steps baked into this deployment's image (author a plan: with check: steps).")
		return nil
	}

	// Deploy overlay → runtime variable resolution (real HOST_PORT mappings,
	// container IP, env), same as `charly check live`.
	var deployOverlay *BundleNode
	if dc := loadDeployConfigForRead("charly check feature run"); dc != nil {
		if entry, ok := dc.Bundle[deployKey(c.Box, c.Instance)]; ok {
			deployOverlay = &entry
		} else if entry, ok := dc.Bundle[c.Box]; ok {
			deployOverlay = &entry
		}
	}
	resolver, _ := ResolveCheckVarsRuntime(meta, deployOverlay, engine, c.Box, containerName, c.Instance)

	filter, err := planTagFilter(c.Tag)
	if err != nil {
		return fmt.Errorf("parsing --tag: %w", err)
	}

	runner := NewRunner(ContainerChain(engine, containerName), resolver, RunModeLive)
	// ADE acceptance: verify the deployed result via check:/agent-check: and
	// grade agent-run:; do NOT re-run the build-time install (run:) steps —
	// they already provisioned the image and reference build-only context.
	runner.SkipDeterministicRun = true
	// Shared identity + committed-APK anchoring (CandyDirs) — the SAME wiring
	// `charly check live` uses, so an adb/appium `apk:` check resolves its fixture
	// here too (R3). Omitting it left feature run scanning 0 candies → the apk
	// failed to anchor only under feature run.
	attachCheckRunnerContext(runner, c.Box, c.Instance, meta.Distro, dir, projectCfg)

	// Wire the agent grader for prose-only steps unless deterministic-only.
	if !c.NoAgent {
		ai, aerr := resolveGraderAgent(dir, c.Agent)
		if aerr != nil {
			return aerr
		}
		runner.Grader = &AgentGrader{Agent: ai, Target: c.Box, Instance: c.Instance, Timeout: c.Timeout}
	}

	results := RunPlan(context.Background(), runner, meta.Description, filter, c.Strict)

	grading := "agent-graded prose"
	if c.NoAgent {
		grading = "deterministic-only"
	}
	fmt.Fprintf(os.Stderr, "Feature run (deploy scope, %s): %s (container: %s)\n", grading, meta.Box, containerName)
	fails := reportSteps(os.Stdout, results, c.Format)
	if fails > 0 {
		return &CheckFailedError{Failed: fails}
	}
	return nil
}
