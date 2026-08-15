package check

// feature_box_gather.go — the "feature-box" check-run mode: BUILD-scope Agent Driven Evaluation
// acceptance against a DISPOSABLE container (the `charly box feature run <image>` engine, relocated
// from the former core hostFeatureBox in cone-C #31). It mirrors run_box.go's
// pluginCheckRunBox (same ResolveRuntime → ResolveLocalImageRef → ExtractMetadata →
// CheckBoxContainerChain → newPluginCheckRunner build-scope shape) but runs the FEATURE plan: the
// whole baked plan with SkipDeterministicRun (skip the build-time install run: steps) + the caller's
// --strict, and validates the --tag expression, exactly as the former core engine did. `charly box
// feature run` reaches it as a plugin-box command (command:feature, CommandParent()=="box")
// InvokeProvider'ing command:check's hidden `__feature-box` leaf, which routes here via Mode:
// "feature-box" — the F10 plugin↔plugin peer-dispatch bridge (the same shape command:build →
// build:ensure uses). The former core check_feature_run.go BoxFeatureRunCmd + hostFeatureBox are
// DELETED with this move.

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// CheckFeatureBoxCmd is the HIDDEN `charly check __feature-box <image>` leaf — the build-scope
// counterpart of CheckFeatureRunCmd (feature_cmd.go). It is NOT a user surface: `charly box feature
// run <image>` (candy/plugin-box's command:feature, CommandParent()=="box") InvokeProvider's
// command:check with args ["__feature-box", <image>, <flags>], routing here so the build-scope ADE
// acceptance runs in plugin-check over Mode:"feature-box" (the F10 plugin↔plugin bridge, cone-C #31).
// Behaviour + flags + output are byte-equivalent to the former in-core BoxFeatureRunCmd.
type CheckFeatureBoxCmd struct {
	Image  string `arg:"" help:"Image reference: a full ref, '<box>:<calver>' to pin one build, or a bare short name resolved against local container storage (refused when a newer local build of that box exists)"`
	Format string `name:"format" default:"text" help:"Output format: text, json, tap, junit"`
	Tag    string `name:"tag" help:"Only run steps matching this tag expression (e.g. 'smoke and not slow')"`
	Strict bool   `name:"strict" help:"Treat prose-only (unbound) steps as failures instead of skips"`
}

func (c *CheckFeatureBoxCmd) Run() error {
	reply, err := hostCheckRun(spec.CheckRunRequest{Mode: "feature-box", Image: c.Image, Tag: c.Tag, Strict: c.Strict})
	if err != nil {
		return err
	}
	if reply.NoSteps {
		fmt.Fprintln(os.Stderr, "No plan steps baked into this image (author a plan: with check: steps).")
		return nil
	}
	if reply.Header != "" {
		fmt.Fprintln(os.Stderr, reply.Header)
	}
	reportSteps(os.Stdout, reply.Steps, c.Format)
	return failErrorFor(reply.Steps)
}

// pluginCheckRunFeatureBox is the "feature-box" mode: build-scope ADE acceptance against a disposable
// container. Byte-equivalent to the former core hostFeatureBox (deterministic steps against the baked
// plan; prose-only steps stay advisory-skip unless --strict). No agent grader — a disposable
// build-scope container has no stable live target to probe (that is the deploy-scope feature-live
// mode's job).
func pluginCheckRunFeatureBox(ex *sdk.Executor, ctx context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	// The build-scope ADE acceptance run is a verdict on a built artifact — same guard as
	// `charly check box` (kit.ResolveBuiltImageRef refuses a stale short-name election).
	imageRef, err := kit.ResolveBuiltImageRef(rt.RunEngine, req.Image)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	meta, err := deploykit.ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		return kit.CheckRunReply{Image: imageRef, NoSteps: true}, nil
	}
	// validateTagExpr still VALIDATES --tag's syntax; applying the parsed filter to the plan walk is a
	// known, tracked gap preserved verbatim from the core original (kit.RunPlan takes no filter param).
	if err := kit.ValidateTagExpr(req.Tag); err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("parsing --tag: %w", err)
	}
	// R44 Option A: ONE persistent container + per-step `podman exec` (CheckBoxContainerChain).
	executor, teardown, err := deploykit.CheckBoxContainerChain(rt.RunEngine, imageRef)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	defer teardown()
	env, hasRuntime := pluginResolverEnv(kit.ResolveCheckVarsBuild(meta))
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      "feature-box",
		Distros:   meta.Distro,
		VenueKind: executor.Kind(),
	}, kit.RunnerConfig{
		Exec:                 executor,
		Mode:                 kit.ModeBox,
		Env:                  env,
		HasRuntime:           hasRuntime,
		Distros:              meta.Distro,
		SkipDeterministicRun: true,
	})
	results := kit.RunPlan(ctx, runner, meta.Description, req.Strict)
	return kit.CheckRunReply{Image: imageRef, Steps: results, Header: fmt.Sprintf("Feature run (image, build scope): %s", imageRef)}, nil
}
