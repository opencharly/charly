package check

// feature_run_gather.go — K1-unblock wave, arm 2 (the "feature-live" check-run mode):
// pluginCheckRunFeatureLive, the plugin-resident port of charly/host_build_check_run.go's
// (deleted) hostFeatureLive, mirroring live_gather.go's pluginCheckLivePod's pod-venue
// construction. Reached from candy/plugin-check's OWN `charly check feature run` CLI leaf
// (feature_cmd.go) via command.go's Mode:"feature-live" short-circuit.
//
// NOTE — "feature-box" was traced and DELIBERATELY NOT ported: Mode:"feature-box" has ZERO live
// callers through the check-run seam — `charly box feature run <image>` (check_feature_run.go's
// BoxFeatureRunCmd, the box-grammar CLI leaf that stays core, see its own header for why) calls
// the CLI-free hostFeatureBox engine DIRECTLY, never through hostCheckRun/HostBuild. Porting a
// plugin-side twin would have been dead code from the moment it landed (unreachable, R3/R4
// dead-code territory) — confirmed by grep before writing it, then deleted after writing it once
// the unreachability was traced. hostFeatureBox stays core, unmoved, still serving its one real
// caller.
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
	var venueDesc *spec.VenueDescriptor
	if d := kit.DescriptorFromExecutor(execChain); d.Kind != "" {
		venueDesc = &d
	}
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      "feature-live",
		Box:       req.Name,
		Instance:  req.Instance,
		Distros:   meta.Distro,
		VenueKind: execChain.Kind(),
	}, venueDesc, kit.RunnerConfig{
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
