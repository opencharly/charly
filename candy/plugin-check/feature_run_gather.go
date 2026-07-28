package check

// feature_run_gather.go — K1-unblock wave, arm 2 (the "feature-live" check-run mode):
// pluginCheckRunFeatureLive, the plugin-resident port of charly/host_build_check_run.go's
// (deleted) hostFeatureLive, mirroring live_gather.go's pluginCheckLivePod's pod-venue
// construction. Reached from candy/plugin-check's OWN `charly check feature run` CLI leaf
// (feature_cmd.go) via command.go's Mode:"feature-live" short-circuit.
//
// The BUILD-scope sibling "feature-box" mode is now feature_box_gather.go's pluginCheckRunFeatureBox
// (cone-C #31 relocated the former core hostFeatureBox here): `charly box feature run <image>` is
// candy/plugin-box's command:feature, which InvokeProvider's command:check's hidden `__feature-box`
// leaf → Mode:"feature-box" → that engine. So both feature modes (deploy-scope feature-live here,
// build-scope feature-box in the sibling file) live plugin-side; the former in-core BoxFeatureRunCmd
// + hostFeatureBox are DELETED.
//
// The agent-grader resolve reuses agent.go's EXISTING resolveAgentSpec (already the
// synccreds.go/runlocal.go call pattern for this exact catalog→exec-spec resolve, over
// Executor.InvokeProvider — R3, no duplicate second copy) fed rp.AgentBodies, the resolved-project
// envelope's projection of the catalog the deleted core-side grader-catalog resolver used to read
// via uf.PluginKinds["agent"].

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// pluginCheckRunFeatureLive is the "feature-live" mode: deploy-scope ADE acceptance against the
// running deployment req.Name, wiring the host-side agent grader (agent.go's resolveAgentSpec)
// unless req.NoAgent. The port of charly/host_build_check_run.go's hostFeatureLive, mirroring
// live_gather.go's pluginCheckLivePod's pod-venue construction (this mode is always a pod/
// container deployment — the core original never classified vm/local/group here either).
func pluginCheckRunFeatureLive(ex *sdk.Executor, ctx context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	engine, containerName, err := deploykit.ResolveContainer(req.Name, req.Instance)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	dir, _ := os.Getwd()
	rp, err := resolvedProject(ex, ctx, dir)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	imageRef := resolveDeployBoxName(rp, req.Name, req.Instance)
	resolvedRef, err := resolveImageRefForEnsure(rp, imageRef)
	if err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("resolving deploy box %q: %w", imageRef, err)
	}
	meta, err := deploykit.ExtractMetadata(engine, resolvedRef)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		return kit.CheckRunReply{NoSteps: true}, nil
	}
	var deployOverlay *spec.BundleNode
	if dc := deploykit.LoadDeployConfigForRead("charly check feature run"); dc != nil {
		if entry, ok := dc.Bundle[deploykit.DeployKey(req.Name, req.Instance)]; ok {
			deployOverlay = &entry
		} else if entry, ok := dc.Bundle[req.Name]; ok {
			deployOverlay = &entry
		}
	}
	resolver, _ := kit.ResolveCheckVarsRuntime(meta, deployOverlay, engine, req.Name, containerName, req.Instance)
	resolver = stampCharlyBin(resolver)
	// validateTagExpr still VALIDATES --tag's syntax; applying the parsed filter to the plan walk
	// is a known, tracked gap — preserved verbatim from the core original.
	if err := kit.ValidateTagExpr(req.Tag); err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("parsing --tag: %w", err)
	}

	checkLoadPlugins(ex, ctx, req.Name, dir)

	env, hasRuntime := pluginResolverEnv(resolver)
	var grader kit.StepGrader
	if !req.NoAgent {
		ai, aerr := resolveAgentSpec(ex, ctx, rp.AgentBodies, req.Agent)
		if aerr != nil {
			return kit.CheckRunReply{}, aerr
		}
		grader = &kit.AgentGrader{Agent: ai, Target: req.Name, Instance: req.Instance, Timeout: req.Timeout}
	}
	execChain := deploykit.ContainerChain(engine, containerName)
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      "feature-live",
		Box:       req.Name,
		Instance:  req.Instance,
		Distros:   meta.Distro,
		VenueKind: execChain.Kind(),
	}, kit.RunnerConfig{
		Exec:                 execChain,
		Mode:                 kit.ModeLive,
		Env:                  env,
		HasRuntime:           hasRuntime,
		Distros:              meta.Distro,
		Box:                  req.Name,
		Instance:             req.Instance,
		SkipDeterministicRun: true,
		CandyDirs:            candyDirsFromEnvelope(rp),
		Grader:               grader,
	})
	results := kit.RunPlan(ctx, runner, meta.Description, req.Strict)
	grading := "agent-graded prose"
	if req.NoAgent {
		grading = "deterministic-only"
	}
	header := fmt.Sprintf("Feature run (deploy scope, %s): %s (container: %s)", grading, meta.Box, containerName)
	return kit.CheckRunReply{Image: meta.Box, Steps: results, Header: header}, nil
}
