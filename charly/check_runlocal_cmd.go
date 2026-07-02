package main

// check_runlocal_cmd.go — in-target entry point of the harness.
//
// The host-side `charly check run <score>` is a thin forwarder. All real
// work happens here, executed *inside the chosen target* (pod via
// `podman exec`, vm via `ssh`, or host directly).
//
// Responsibilities (in order):
//   1. Acquire .harness/<score>/.lock — per-target concurrency guard
//   2. Resolve the entity's `plan:` (baked + include:'d + inline) to a merged step list
//   3. Clone <project> -> <project>/.check/<score>/runs/<run-id>/repo
//   4. Create branch charlycheck/<run-id> + submodule init
//   5. Synthesize the pre-AI baseline from the merged plan steps
//   6. Drive RunHarness — the iteration state machine
//   7. Push branch back to the bind-mounted/host project repo
//   8. Release lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pinPersistentXDGRuntimeDir relocates `XDG_RUNTIME_DIR` to a persistent
// path under `$HOME` when the current value points at a transient
// `/run/user/<uid>` tmpfs. Crun stores per-container status files at
// `$XDG_RUNTIME_DIR/crun/<id>/status`; if that location is wiped while
// containers are still running, every subsequent `podman exec` against
// those containers fails with "container does not exist" — even though
// the container processes are alive. Forensic evidence from the
// 2026-04-27 N canary: `/run/user/1000` disappeared between phases 6
// and 7, breaking the harness's per-iter `RunCheckLive`
// probes for every pre-existing pod. Pinning to `$HOME/.local/share/charly-runtime`
// (a regular directory on the harness sandbox's persistent overlay) survives
// whatever wipes the tmpfs.
//
// Only relocates when XDG_RUNTIME_DIR is empty or a `/run/user/...`
// path; explicit overrides are respected.
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
	Score       string `arg:"" help:"Score name (from check.yml)"`
	TargetImage string `name:"target-image" help:"Target image to score (default: derived from score / pod)"`
	Agent       string `name:"agent" help:"Agent to invoke (defaults to score.agent when single-element)"`
	RunID       string `name:"run-id" help:"Run identifier (set by host harness; auto if empty)"`
	PlateauIter int    `name:"plateau-iteration" help:"Override score.plateau_iteration"`
	MaxStep     int    `name:"max-step" help:"Cap the pending input set"`
	Tag         string `name:"tag" help:"tag expression to narrow plan steps"`
	DryRun      bool   `name:"dry-run" help:"Render scope+prompt then exit; no AI invocation, no rebuild"`
	SkipRebuild bool   `name:"skip-rebuild" help:"Skip per-iteration rebuild (source-only steps)"`
	Format      string `enum:"text,yaml" default:"text" help:"Report format on stdout"`
	NoLock      bool   `name:"no-lock" hidden:"" help:"Skip flock (tests only)"`
	KeepRepo    bool   `name:"keep-repo" help:"Don't delete the per-run repo clone after the run completes (debugging only — clones are ~100MB)"`
	ProjectDir  string `name:"project-dir" hidden:"" help:"Override project root (default: cwd or /workspace)"`
}

// HarnessLockPath returns the absolute path of the per-score flock
// file under the harness data root.
func HarnessLockPath(projectDir, score string) string {
	return filepath.Join(HarnessDataRoot(projectDir, score), ".lock")
}

func (c *CheckRunLocalCmd) Run() error {
	ctx := context.Background()

	// Pin XDG_RUNTIME_DIR to a persistent location BEFORE any podman
	// operation runs. Every child process the harness spawns — the AI's
	// claude subprocess, its bash subshells, the per-iter probe path's
	// `podman exec` calls — inherits this env. With it pinned, crun
	// status files live on the harness sandbox's overlay (persistent) rather
	// than `/run/user/1000` (transient).
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

	uf, ok, err := LoadUnified(projectDir)
	if err != nil {
		return fmt.Errorf("load harness config from %s: %w", projectDir, err)
	}
	if !ok || uf == nil {
		return fmt.Errorf("charly check run-local: no charly.yml in %s", projectDir)
	}

	node, found := uf.Bundle[c.Score]
	if !found || node.Iterate == nil {
		return fmt.Errorf("charly check run-local: entity %q has no iterate: block", c.Score)
	}
	iterate := node.Iterate
	tk, tn := ResolveIterateSandbox(uf, iterate.Sandbox)

	// Build the entity's scored plan: its own plan: with include: directives
	// expanded against the project candies. The MergedPlan is the AI-facing
	// slice (nonces un-substituted).
	layers, lerr := ScanCandy(projectDir)
	if lerr != nil {
		return fmt.Errorf("scan candies: %w", lerr)
	}
	mergedPlan, err := ExpandPlanIncludes(uf, layers, node.Plan)
	if err != nil {
		return fmt.Errorf("entity %q: expand includes: %w", c.Score, err)
	}

	// Generate per-run nonces and substitute into a SECOND plan for scoring.
	// The AI sees the un-substituted plan via ${PLAN}/${CHECKS}; the
	// substituted plan flows into baseline + per-iter scoring so probes carry
	// real nonce values the AI cannot have pre-set.
	nonces, err := GenerateHarnessNonces(mergedPlan)
	if err != nil {
		return fmt.Errorf("generate harness nonces: %w", err)
	}
	if len(nonces) > 0 {
		names := make([]string, 0, len(nonces))
		for name := range nonces {
			names = append(names, name)
		}
		fmt.Fprintf(os.Stderr,
			"harness: generated %d per-run nonce(s): %v\n", len(nonces), names)
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
	ai, _, err := ResolveAgent(uf.Agents(), aiName)
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

	// No in-pod preflight: the harness only owns the harness sandbox itself
	// (rebuilt fresh per run by the host-side preflight in check_runner_cmd.go).
	// Inside the sandbox, the AI is on its own — it builds whatever images
	// each step needs, creates each pod a step references via `charly bundle
	// add`, and modifies state until check: steps pass. The harness scoring
	// code probes per step.Op.Pod after the AI exits.

	tagExpr := c.Tag
	plateau := c.PlateauIter
	if plateau == 0 {
		plateau = iterate.PlateauIteration
	}

	notesSnap := ""
	if iterate.NotesEnabled() {
		notesSnap, _ = ReadNote(projectDir, c.Score)
	}

	mcp := iterateEffectiveMCPEndpoint(iterate)

	aiVer := LocalCaptureVersion(ctx, ai)

	// commonOpts captures everything that doesn't change across iterations.
	commonOpts := HarnessOpts{
		ProjectDir:       projectDir,
		ScoreName:        c.Score,
		Iterate:          iterate,
		TargetKind:       string(tk),
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
	mergedPlan []Step,
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

// acquireHarnessLock takes a fail-fast exclusive flock on the per-score lock
// file via the shared acquireFileLock primitive (filelock.go).
func acquireHarnessLock(projectDir, score string) (func(), error) {
	path := HarnessLockPath(projectDir, score)
	release, err := acquireFileLock(path, false)
	if err != nil {
		if errors.Is(err, errLockBusy) {
			return nil, fmt.Errorf("harness: another run is in progress for score %q (lock: %s)", score, path)
		}
		return nil, err
	}
	return func() { _ = release() }, nil
}

// loadDescriptionsFromDir is retained for the (deprecated) image-baked
// path; the score-based flow uses synthesizeScoreBaseline instead.
func loadDescriptionsFromDir(dir, image string) *LabelDescriptionSet {
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		return nil
	}
	defer func() { _ = os.Chdir(origWd) }()

	cfg, err := LoadConfig(dir)
	if err != nil || cfg == nil {
		return nil
	}
	layers, err := ScanCandy(dir)
	if err != nil {
		return nil
	}
	return CollectDescriptions(cfg, layers, image)
}
