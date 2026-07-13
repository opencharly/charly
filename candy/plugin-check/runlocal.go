package check

// runlocal.go — the in-target entry point of the harness (P12: relocated from
// charly/check_runlocal_cmd.go).
//
// The host-side `charly check run <score>` is a thin forwarder; all real work happens
// here, executed *inside the chosen target* (pod via `podman exec`, vm via `ssh`, or
// host directly). Responsibilities: acquire the in-sandbox flock, resolve the
// entity's plan (via the "check-config" host seam — the loader stays core), clone the
// project, synthesize the pre-AI baseline, drive RunHarness, push the branch back.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// pinPersistentXDGRuntimeDir relocates XDG_RUNTIME_DIR to a persistent path under
// $HOME when the current value points at a transient /run/user/<uid> tmpfs. Crun
// stores per-container status files there; if that tmpfs is wiped while containers
// run, every subsequent `podman exec` fails with "container does not exist". Only
// relocates when XDG_RUNTIME_DIR is empty or a /run/user/... path.
func pinPersistentXDGRuntimeDir() error {
	current := os.Getenv("XDG_RUNTIME_DIR")
	if current != "" && !strings.HasPrefix(current, "/run/user/") {
		return nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "/home/user"
	}
	persistent := filepath.Join(home, ".local", "share", "charly-runtime")
	if err := os.MkdirAll(persistent, 0o700); err != nil {
		return fmt.Errorf("creating persistent runtime dir %s: %w", persistent, err)
	}
	if err := os.Setenv("XDG_RUNTIME_DIR", persistent); err != nil {
		return fmt.Errorf("setting XDG_RUNTIME_DIR=%s: %w", persistent, err)
	}
	return nil
}

// CheckRunLocalCmd drives the iteration loop in the chosen target.
type CheckRunLocalCmd struct {
	Score       string `arg:"" help:"Score name (from the iterate: entity)"`
	TargetImage string `name:"target-image" help:"Target image to score (default: derived from score / pod)"`
	Agent       string `name:"agent" help:"Agent to invoke (defaults to the entity's agent when single-element)"`
	RunID       string `name:"run-id" help:"Run identifier (set by host harness; auto if empty)"`
	PlateauIter int    `name:"plateau-iteration" help:"Override plateau_iteration"`
	MaxStep     int    `name:"max-step" help:"Cap the pending input set"`
	Tag         string `name:"tag" help:"tag expression to narrow plan steps"`
	DryRun      bool   `name:"dry-run" help:"Render scope+prompt then exit; no AI invocation, no rebuild"`
	SkipRebuild bool   `name:"skip-rebuild" help:"Skip per-iteration rebuild (source-only steps)"`
	Format      string `enum:"text,yaml" default:"text" help:"Report format on stdout"`
	NoLock      bool   `name:"no-lock" hidden:"" help:"Skip flock (tests only)"`
	KeepRepo    bool   `name:"keep-repo" help:"Don't delete the per-run repo clone after the run completes (debugging only — clones are ~100MB)"`
	ProjectDir  string `name:"project-dir" hidden:"" help:"Override project root (default: cwd or /workspace)"`
}

// HarnessLockPath returns the absolute path of the per-score flock file under the
// harness data root.
func HarnessLockPath(projectDir, score string) string {
	return filepath.Join(HarnessDataRoot(projectDir, score), ".lock")
}

func (c *CheckRunLocalCmd) Run() error {
	ctx := context.Background()

	// Pin XDG_RUNTIME_DIR to a persistent location BEFORE any podman operation.
	if err := pinPersistentXDGRuntimeDir(); err != nil {
		return err
	}

	projectDir := c.ProjectDir
	if projectDir == "" {
		if _, err := os.Stat("/workspace"); err == nil {
			projectDir = "/workspace"
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			projectDir = cwd
		}
	}

	// Resolve the entity's config + scored plan over the "check-config" host seam
	// (LoadUnified + ScanCandy + ExpandPlanIncludes are core loader Mechanisms).
	reply, err := checkConfig(cmdExec, cmdCtx, spec.CheckConfigRequest{Entity: c.Score, Dir: projectDir})
	if err != nil {
		return fmt.Errorf("load harness config from %s: %w", projectDir, err)
	}
	if !reply.HasNode {
		return fmt.Errorf("charly check run-local: no charly.yml in %s", projectDir)
	}
	if !reply.HasIterate {
		return fmt.Errorf("charly check run-local: entity %q has no iterate: block", c.Score)
	}
	var iterate spec.Iterate
	if len(reply.IterateJSON) > 0 {
		if err := json.Unmarshal(reply.IterateJSON, &iterate); err != nil {
			return fmt.Errorf("charly check run-local: decode iterate: %w", err)
		}
	}
	tk, tn := reply.SandboxKind, reply.SandboxName

	// The entity's scored plan (its own plan: with include: expanded against the
	// project candies) — the AI-facing slice (nonces un-substituted).
	mergedPlan := reply.Plan

	// Generate per-run nonces and substitute into a SECOND plan for scoring: the AI
	// sees the un-substituted plan via ${PLAN}/${CHECKS}; the substituted plan flows
	// into baseline + per-iter scoring so probes carry real nonce values.
	nonces, err := GenerateHarnessNonces(mergedPlan)
	if err != nil {
		return fmt.Errorf("generate harness nonces: %w", err)
	}
	if len(nonces) > 0 {
		names := make([]string, 0, len(nonces))
		for name := range nonces {
			names = append(names, name)
		}
		fmt.Fprintf(os.Stderr, "harness: generated %d per-run nonce(s): %v\n", len(nonces), names)
	}

	// AI selection — iterate.Agent is the eligible list; --agent picks one.
	aiName := c.Agent
	if aiName == "" {
		switch len(iterate.Agent) {
		case 1:
			aiName = iterate.Agent[0]
		case 0:
			return fmt.Errorf("iterate entity %q has empty agent: list", c.Score)
		default:
			return fmt.Errorf("iterate entity %q has multiple eligible agents (%v); pass --agent NAME", c.Score, iterate.Agent)
		}
	}
	ai, err := resolveAgentSpec(cmdExec, cmdCtx, reply.AgentBodies, aiName)
	if err != nil {
		return err
	}

	if !c.NoLock {
		unlock, lerr := acquireHarnessLock(projectDir, c.Score)
		if lerr != nil {
			return lerr
		}
		defer unlock()
	}

	layout := NewRunLayout(projectDir, c.Score, c.RunID)
	if err := os.MkdirAll(layout.RunDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", layout.RunDir, err)
	}
	fmt.Fprintf(os.Stderr, "harness: score=%s ai=%s run=%s where=%s:%s\n",
		c.Score, aiName, layout.RunID, tk, tn)

	if err := CreateRunClone(ctx, layout); err != nil {
		return fmt.Errorf("clone %s -> %s: %w", projectDir, layout.RepoDir, err)
	}

	targetImage := c.TargetImage

	tagExpr := c.Tag
	plateau := c.PlateauIter
	if plateau == 0 {
		plateau = iterate.PlateauIteration
	}

	notesSnap := ""
	if iterate.NotesEnabled() {
		notesSnap, _ = ReadNote(projectDir, c.Score)
	}

	mcp := iterateEffectiveMCPEndpoint(&iterate)

	aiVer := LocalCaptureVersion(ctx, ai)

	// commonOpts captures everything that doesn't change across iterations.
	commonOpts := HarnessOpts{
		ProjectDir:       projectDir,
		ScoreName:        c.Score,
		Iterate:          &iterate,
		TargetKind:       tk,
		TargetName:       tn,
		AgentName:        aiName,
		Agent:            ai,
		Prompt:           iterate.Prompt,
		TargetImage:      targetImage,
		Tag:              tagExpr,
		PlateauIteration: plateau,
		MaxStep:          c.MaxStep,
		MCPEndpoint:      mcp,
		Notes:            notesSnap,
		Deploy:           c.Score,
		DryRun:           c.DryRun,
		SkipRebuild:      c.SkipRebuild,
		Format:           c.Format,
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
	}

	report, err := runSinglePhaseHarness(ctx, layout, commonOpts, mergedPlan, nonces)
	if err != nil {
		return err
	}
	report.AgentVersion = map[string]string{aiName: aiVer.String()}

	if err := PushBranchToHost(ctx, layout); err != nil {
		fmt.Fprintf(os.Stderr, "harness: push branch back failed (non-fatal): %v\n", err)
	}

	_ = writeReport(layout, report)

	if !c.KeepRepo {
		if err := os.RemoveAll(layout.RepoDir); err != nil {
			fmt.Fprintf(os.Stderr, "harness: cleanup of %s failed (non-fatal): %v\n", layout.RepoDir, err)
		}
	}

	printHarnessReport(os.Stdout, report, c.Format)
	return nil
}

// runSinglePhaseHarness drives one RunHarness pass over the entity's plan.
func runSinglePhaseHarness(
	ctx context.Context,
	layout RunLayout,
	commonOpts HarnessOpts,
	mergedPlan []spec.Step,
	nonces map[string]string,
) (*FinalReport, error) {
	scoringPlan, err := SubstituteStepNonces(mergedPlan, nonces)
	if err != nil {
		return nil, fmt.Errorf("substitute harness nonces: %w", err)
	}
	preAIResults, preFingerprints, preTagFingerprints := synthesizeScoreBaseline(commonOpts.ScoreName, scoringPlan)
	opts := commonOpts
	opts.MergedPlan = mergedPlan
	opts.ScoringPlan = scoringPlan
	opts.PreAIStep = preAIResults
	opts.PreFingerprints = preFingerprints
	opts.PreTagFingerprints = preTagFingerprints
	return RunHarness(ctx, opts, layout)
}

// acquireHarnessLock takes a fail-fast exclusive flock on the per-score lock file. It
// runs INSIDE the sandbox, so it uses the stdlib syscall.Flock directly (the core's
// acquireFileLock is package-main-only; K2 statekit later owns any-process state).
func acquireHarnessLock(projectDir, score string) (func(), error) {
	path := HarnessLockPath(projectDir, score)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("harness: mkdir lock dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("harness: open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("harness: another run is in progress for score %q (lock: %s)", score, path)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// loadDescriptionsFromDir is the (deprecated) image-baked-fingerprint path: it wraps
// the include-expanded plan (resolved over the "check-config" seam — the loader stays
// core) into a LabelDescriptionSet keyed by the "plan" origin so the derived step ids
// align with the "score"-mode scoring ids. The score-based live flow uses
// synthesizeScoreBaseline instead, so this only feeds the source-only rebuild path
// (dead for iterate entities, which always carry a ScoringPlan).
func loadDescriptionsFromDir(ctx context.Context, score string) *kit.LabelDescriptionSet {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	reply, err := checkConfig(cmdExec, ctx, spec.CheckConfigRequest{Entity: score, Dir: cwd})
	if err != nil || len(reply.Plan) == 0 {
		return nil
	}
	return &kit.LabelDescriptionSet{Candy: []kit.LabeledDescription{{Origin: scoredPlanOrigin, Plan: reply.Plan}}}
}
