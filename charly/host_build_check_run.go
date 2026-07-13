package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// host_build_check_run.go — the generic "check-run" F10 host-builder (P12). command:check
// (candy/plugin-check) owns the `charly check` CLI + output formatting, but RUNNING a plan
// against a venue is a composite of core host-serving Mechanisms a plugin (a separate module
// importing only sdk) cannot perform: the venue→executor construction, the OCI-label plan
// extraction (ExtractMetadata), and the plan-walk's verb dispatch through the provider registry
// (the in-core VerbResolver). So the plugin resolves its intent into a spec.CheckRunRequest and
// the host builds the venue + runs the kit-Runner + returns the per-step results — exactly as
// command:vm forwards `vm build` to HostBuild("vm-build"). The action noun is CLASS-GENERIC
// ("check-run"), never a substrate word (the F11 uniform-API gate).
//
// It returns RESULT DATA only (the []StepResult the plugin formats + tallies into an exit
// code); the plugin owns the CLI parse, the "Image:" header, the text/yaml/tap reporters, and
// the exit-code mapping (CheckFailExitCode). The engine (Runner / plan walk / registry dispatch)
// stays a host-serving atom, consumed here.
const checkRunBuilderKind = "check-run"

func hostBuildCheckRun(ctx context.Context, req spec.CheckRunRequest, _ buildEngineContext) (kit.CheckRunReply, error) {
	switch req.Mode {
	case "box":
		return hostCheckRunBox(ctx, req)
	case "live":
		return hostCheckRunLive(ctx, req)
	case "feature-box":
		return hostCheckRunFeatureBox(ctx, req)
	case "feature-live":
		return hostCheckRunFeatureLive(ctx, req)
	case "score":
		return hostCheckRunScore(ctx, req)
	case "preflight":
		return hostCheckRunPreflight(ctx, req)
	default:
		return kit.CheckRunReply{}, fmt.Errorf("check-run: unknown mode %q", req.Mode)
	}
}

// hostCheckRunScore is the "score" atom arm (P12 Wave-2 AI harness): it walks the SUBSTITUTED
// scoring plan carried in req.Plan (nonce-carrying, NOT the OCI-baked plan the "live" mode
// extracts) against the live deployments its check:/agent-check: steps target via each step's
// loader-derived Op.Venue, returning the per-step verdicts in reply.Score. RunCheckLive stays a
// host-serving atom (its topo/pod-bucket/ephemeral-wrap build is registry/venue-coupled); the
// plugin harness owns the scoring math (Classify, fingerprints) over the returned *CheckRunResults.
func hostCheckRunScore(ctx context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	results, err := RunCheckLive(ctx, req.Name, req.Name, req.Plan)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	return kit.CheckRunReply{Score: results}, nil
}

// hostCheckRunPreflight is the "preflight" atom arm: for a host-target iterate entity, ensure every
// image the score's plan steps spawn is present in local storage BEFORE the harness runner walks
// them. The include-EXPANDED scored plan is computed PLUGIN-SIDE off the resolved-project envelope
// (candy/plugin-check's include-splicer, which owns the plan expansion) and threaded here as
// req.Plan; the host runs the R3-shared EnsureImagePresent (via ensureScoreImages) over it and
// returns an empty reply.
func hostCheckRunPreflight(_ context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	dir := req.Dir
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if !ok || uf == nil {
		return kit.CheckRunReply{}, fmt.Errorf("check-run preflight: no charly.yml in %s", dir)
	}
	if _, has := uf.Bundle[req.Name]; !has {
		return kit.CheckRunReply{}, fmt.Errorf("check-run preflight: no entity %q in %s", req.Name, dir)
	}
	if err := ensureScoreImages(context.Background(), req.Plan, uf, dir); err != nil {
		return kit.CheckRunReply{}, err
	}
	return kit.CheckRunReply{}, nil
}

// hostCheckRunBox runs a pure-box check: a disposable container built from the image, build-scope
// steps only (RunModeBox). It is CheckBoxCmd.Run's engine (ExtractMetadata → ImageChain venue →
// NewRunner → RunPlan) minus the CLI parse + the text/yaml reporters (which live in the plugin).
// The reply carries []StepResult verbatim so the plugin's kit formatters produce byte-identical
// output to the former in-core CheckBoxCmd.
func hostCheckRunBox(_ context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	imageRef, err := resolveLocalImageRef(rt.RunEngine, req.Image)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	meta, err := ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		return kit.CheckRunReply{Image: imageRef, NoSteps: true}, nil
	}

	// PURE-BOX: always a disposable container, build-context steps only (RunModeBox skips
	// deploy/runtime-context steps). No autodetect, no fallback — the mode is explicit.
	// Mirrors CheckBoxCmd.Run's engine exactly (minus the CLI parse + reporters, which live
	// in candy/plugin-check), so the reply's []StepResult formats byte-identically.
	executor := ImageChain(rt.RunEngine, imageRef)
	resolver := ResolveCheckVarsBuild(meta)
	env, hasRuntime := resolverEnv(resolver)
	runner := newCheckRunner(kit.RunnerConfig{
		Exec:       executor,
		Mode:       RunModeBox,
		Env:        env,
		HasRuntime: hasRuntime,
		Distros:    meta.Distro,
		VerifyOnly: true,
	})

	stepResults := RunPlan(context.Background(), runner, meta.Description, nil, false)
	return kit.CheckRunReply{Image: imageRef, Steps: stepResults}, nil
}

// hostCheckRunLive is the "live" atom arm: a full-stack run against a running deployment
// resolved by req.Name. The host classifies vm/pod/local/group INTERNALLY (checkLiveGather),
// so the plugin stays kind-blind — it sends ONE Mode:"live" and gets back the pre-formatted
// kind-specific Header + Steps (or, for a nested-pod-in-VM leaf, the verbatim guest Passthrough).
func hostCheckRunLive(_ context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	return hostCheckLive(req)
}

// hostCheckLive is the CLI-free live engine: it classifies req.Name and runs the matching
// per-path gather (checkLivePod/VM/Local/Group via checkLiveGather), then maps the internal
// liveResult onto the wire reply. It is the SINGLE source the atom arm (here) and the CLI
// (CheckLiveCmd.Run → runPod/runVm/runLocalCheck/runGroupCheck) share. The request carries no
// format field — the plugin formats the returned Steps itself — so a nested-pod guest defaults
// to text here (the interactive CLI preserves --format via c.Format on its own path).
func hostCheckLive(req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	c := &CheckLiveCmd{Box: req.Name, Instance: req.Instance, Section: req.Section, Filter: req.Filter}
	res, err := c.checkLiveGather()
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	reply := kit.CheckRunReply{Steps: res.Steps, Header: res.Header, Passthrough: res.Passthrough}
	if res.NoPlan {
		// The kind-specific no-plan message (pod "…defined for this image." vs vm/local/group
		// "…to run.") normalizes to the wire NoSteps flag; the plugin prints its own line.
		reply.NoSteps = true
	}
	return reply, nil
}

// hostCheckRunFeatureBox is the "feature-box" atom arm: build-scope ADE acceptance over
// req.Image (SkipDeterministicRun; no grader — prose-only steps stay advisory).
func hostCheckRunFeatureBox(_ context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	return hostFeatureBox(req)
}

// hostFeatureBox is the CLI-free engine of BoxFeatureRunCmd.Run: run the image's baked plan
// against a disposable container, deterministic steps only (SkipDeterministicRun skips the
// build-time install run: steps). Shared by the atom arm and the CLI shell (BoxFeatureRunCmd).
func hostFeatureBox(req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	imageRef, err := resolveLocalImageRef(rt.RunEngine, req.Image)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	meta, err := ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		return kit.CheckRunReply{Image: imageRef, NoSteps: true}, nil
	}
	filter, err := planTagFilter(req.Tag)
	if err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("parsing --tag: %w", err)
	}
	env, hasRuntime := resolverEnv(ResolveCheckVarsBuild(meta))
	runner := newCheckRunner(kit.RunnerConfig{
		Exec:                 ImageChain(rt.RunEngine, imageRef),
		Mode:                 RunModeBox,
		Env:                  env,
		HasRuntime:           hasRuntime,
		Distros:              meta.Distro,
		SkipDeterministicRun: true,
	})
	results := RunPlan(context.Background(), runner, meta.Description, filter, req.Strict)
	return kit.CheckRunReply{Image: imageRef, Steps: results, Header: fmt.Sprintf("Feature run (image, build scope): %s", imageRef)}, nil
}

// hostCheckRunFeatureLive is the "feature-live" atom arm: deploy-scope ADE acceptance against
// the running deployment req.Name, wiring the host-side agent grader (resolveGraderAgent →
// AgentGrader) unless req.NoAgent.
func hostCheckRunFeatureLive(_ context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	return hostFeatureLive(req)
}

// hostFeatureLive is the CLI-free engine of CheckFeatureRunCmd.Run: run the deployment image's
// baked plan against the running container, deterministic check: steps + the host-side agent
// grader for prose-only steps (unless req.NoAgent). The grader runs INSIDE the host-side plan
// walk (it needs LoadUnified + the kind:agent CLI), so it stays behind the seam. Shared by the
// atom arm and the CLI shell (CheckFeatureRunCmd).
func hostFeatureLive(req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	engine, containerName, err := resolveContainer(req.Name, req.Instance)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	dir, _ := os.Getwd()
	var projectCfg *Config
	if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
		projectCfg = uf.ProjectConfig()
	}
	imageRef := resolveDeployBoxName(req.Name, req.Instance)
	resolvedRef, err := resolveImageRefForEnsure(imageRef, projectCfg, dir)
	if err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("resolving deploy box %q: %w", imageRef, err)
	}
	meta, err := ExtractMetadata(engine, resolvedRef)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		return kit.CheckRunReply{NoSteps: true}, nil
	}
	var deployOverlay *BundleNode
	if dc := loadDeployConfigForRead("charly check feature run"); dc != nil {
		if entry, ok := dc.Bundle[deployKey(req.Name, req.Instance)]; ok {
			deployOverlay = &entry
		} else if entry, ok := dc.Bundle[req.Name]; ok {
			deployOverlay = &entry
		}
	}
	resolver, _ := ResolveCheckVarsRuntime(meta, deployOverlay, engine, req.Name, containerName, req.Instance)
	filter, err := planTagFilter(req.Tag)
	if err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("parsing --tag: %w", err)
	}
	rctx := resolveCheckRunnerContext(req.Name, dir, projectCfg)
	env, hasRuntime := resolverEnv(resolver)
	var grader StepGrader
	if !req.NoAgent {
		ai, aerr := resolveGraderAgent(dir, req.Agent)
		if aerr != nil {
			return kit.CheckRunReply{}, aerr
		}
		grader = &AgentGrader{Agent: ai, Target: req.Name, Instance: req.Instance, Timeout: req.Timeout}
	}
	runner := newCheckRunner(kit.RunnerConfig{
		Exec:                 ContainerChain(engine, containerName),
		Mode:                 RunModeLive,
		Env:                  env,
		HasRuntime:           hasRuntime,
		Distros:              meta.Distro,
		Box:                  req.Name,
		Instance:             req.Instance,
		SkipDeterministicRun: true,
		CandyDirs:            rctx.CandyDirs,
		CandyScanErr:         rctx.CandyScanErr,
		Grader:               grader,
	})
	results := RunPlan(context.Background(), runner, meta.Description, filter, req.Strict)
	grading := "agent-graded prose"
	if req.NoAgent {
		grading = "deterministic-only"
	}
	header := fmt.Sprintf("Feature run (deploy scope, %s): %s (container: %s)", grading, meta.Box, containerName)
	return kit.CheckRunReply{Image: meta.Box, Steps: results, Header: header}, nil
}

// Register the check-run host-builder at package-var init (before any init(), like the
// config-resolve / cli / vm-build builders).
var _ = func() bool {
	registerHostBuilder(checkRunBuilderKind, typedHostBuilder(checkRunBuilderKind, hostBuildCheckRun))
	return true
}()
