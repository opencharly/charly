package main

// check_feature_run.go — the `charly box feature run <image>` Agent Driven
// Evaluation (ADE) acceptance runner (the core box-grammar CLI leaf).
//
// The DEPLOY-scope `charly check feature run <deployment>` CLI leaf moved to the
// compiled-in command:check plugin (candy/plugin-check); its CLI-free engine
// (pluginCheckRunFeatureLive, wiring the host-side agent grader via agent.go's
// resolveAgentSpec) lives entirely plugin-side now
// (candy/plugin-check/feature_run_gather.go, K1-unblock wave arm 2) — including the
// grader-catalog resolution the former core-side helpers provided; the core
// engine + its dedicated grader-catalog resolver were deleted with their one
// caller. This file keeps only the BUILD-scope `charly box feature run` leaf, which
// stays in the box grammar (image.go). The former reportSteps/stepFailCount/
// validateTagExpr helpers dissolved into kit.ReportStepResultsCount /
// kit.ValidateTagExpr (CHECK-wave) — all three were pure wrappers with zero
// core-state coupling.
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
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

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
	// Direct in-process call to the CLI-free engine (hostFeatureBox, host_build_check_run.go) —
	// the box grammar stays core (see the package header), so this never crosses the check-run
	// HostBuild seam. Mode:"feature-box" has no OTHER caller (traced during K1-unblock wave arm
	// 2 — `charly check feature run` is deploy-scope only, Mode:"feature-live") — this call site
	// is the ONE live consumer of hostFeatureBox.
	reply, err := hostFeatureBox(spec.CheckRunRequest{Mode: "feature-box", Image: c.Image, Tag: c.Tag, Strict: c.Strict})
	if err != nil {
		return err
	}
	if reply.NoSteps {
		fmt.Fprintln(os.Stderr, "No plan steps baked into this image (author a plan: with check: steps).")
		return nil
	}
	fmt.Fprintln(os.Stderr, reply.Header)
	fails := kit.ReportStepResultsCount(os.Stdout, reply.Steps, c.Format)
	if fails > 0 {
		return &sdk.ExitCodeError{Code: sdk.CheckFailExitCode, Err: fmt.Errorf("%d check(s) failed", fails)}
	}
	return nil
}
