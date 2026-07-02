package main

// check_runner_cmd.go — `charly check` command tree (host-side dispatcher).
//
// Post the plan-unify cutover, the runner dispatches on an entity's
// `iterate:` block (the AI loop, scoring the entity's own plan: steps) or a
// plain kind:check bed (the deterministic R10 sequence).

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
)

// The HarnessCmd top-level type was deleted in the check-cutover. Its
// fields are absorbed into CheckCmd in check_cmd.go; the per-subcommand
// types (CheckListAgentCmd, CheckRunCmd, CheckSyncCredCmd, CheckScopeCmd,
// CheckLastTagCmd, CheckSelfCheckCmd, CheckListRunsCmd, CheckReportCmd,
// CheckNoteCmd, CheckRunLocalCmd) live in this file (and the
// check_runlocal_cmd.go / check_synccreds_cmd.go siblings).

// ---------------------------------------------------------------------------
// list-ai — functional inspection
// ---------------------------------------------------------------------------

// CheckListAgentCmd implements `charly check list-ai`.
type CheckListAgentCmd struct{}

func (c *CheckListAgentCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return err
	}
	if uf == nil {
		fmt.Fprintln(os.Stdout, "No charly.yml found in current directory.")
		return nil
	}
	PrintAgents(os.Stdout, uf.Agents())
	return nil
}

// ---------------------------------------------------------------------------
// run — host-side dispatcher
// ---------------------------------------------------------------------------

// CheckRunCmd is `charly check run <name>` — overloaded by the kind the name
// resolves to: a kind:check bed runs the full R10 sequence (build → check
// box → deploy → check live → fresh update → tear down); a kind:score
// drives the AI iteration loop. A check bed is a `disposable: true` bundle, so the
// loader's globally-unique node names keep a bed and a score name disjoint.
type CheckRunCmd struct {
	Name  string `arg:"" optional:"" help:"kind:check bed (full R10 sequence) or kind:score (AI iteration loop)."`
	Agent string `name:"agent" help:"Pick which agent to run (required if score.agent has more than one entry)"`

	// kind:check bed-path flags (ignored on the kind:score path).
	Keep      bool `name:"keep" help:"kind:check beds: don't tear the bed down after the run"`
	NoRebuild bool `name:"no-rebuild" help:"kind:check beds: skip the fresh-update R10 re-verify step (R10 acceptance gate)"`

	// Mutually-exclusive target overrides (kind:score path).
	Pod  string `name:"on-pod" xor:"target" help:"Override score target with this pod deployment"`
	VM   string `name:"on-vm" xor:"target" help:"Override score target with this VM"`
	Host bool   `name:"on-host" xor:"target" help:"Override score target to run on the host directly"`

	PlateauIteration int    `name:"plateau-iteration" help:"Override score.plateau_iteration"`
	MaxStep          int    `name:"max-step" help:"Cap the pending input set"`
	Tag              string `name:"tag" help:"Override score.tag (tag expression)"`
	DryRun           bool   `name:"dry-run" help:"Render scope+prompt without rebuild"`
	SkipRebuild      bool   `name:"skip-rebuild" help:"Source-only steps"`
	KeepRepo         bool   `name:"keep-repo" help:"Don't delete the per-run repo clone after the run (~100MB; debugging only)"`
	Format           string `name:"format" enum:"text,yaml" default:"text" help:"Output format"`
}

func (c *CheckRunCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	uf, ok, err := LoadUnified(cwd)
	if err != nil {
		return err
	}
	if !ok || uf == nil {
		return fmt.Errorf("charly check run: no charly.yml in %s", cwd)
	}

	// Reusable-artifact retention: after the run completes (any path — bed
	// or score), trim .check to defaults.keep_check_runs. Runs after
	// the new run's output is written, so the newest run is kept; NOTES.md is
	// always preserved. keep_check_runs: 0 / absent disables. See `charly clean`.
	if keep := resolveIntPtr(uf.Defaults.KeepCheckRuns, nil, keepCheckRunsFallback); keep > 0 {
		defer func() {
			if removed, e := pruneCheckRuns(filepath.Join(cwd, ".check"), keep, false); e == nil && len(removed) > 0 {
				fmt.Fprintf(os.Stderr, "Pruned %d old check run artifact(s) (keep_check_runs=%d)\n", len(removed), keep)
			}
		}()
	}

	// Dispatch: an entity carrying an `iterate:` block → the AI loop; a plain
	// kind:check bed → the deterministic R10 sequence. The two share the
	// `charly check run` verb; ONE bed per invocation.
	beds := uf.CheckBeds()
	if c.Name == "" {
		// Run a whole roster by fanning beds out at the AGENT layer — one
		// `charly check run <bed>` per agent (the /verify-beds workflow / an agent
		// team), which collapses wall-clock to ≈ the slowest single bed instead of
		// the sum of a sequential sweep. See /charly-check:check + /charly-internals:agents.
		return fmt.Errorf("charly check run: provide an iterate: entity or a kind:check bed name (run a full roster concurrently via the /verify-beds workflow)")
	}
	node, hasNode := uf.Bundle[c.Name]
	bedNode, isBed := beds[c.Name]
	if (!hasNode || node.Iterate == nil) && isBed {
		exe, exeErr := os.Executable()
		if exeErr != nil {
			exe = os.Args[0]
		}
		res, runErr := runCheckBed(exe, c.Name, bedNode, bedRunOpts{Keep: c.Keep, NoRebuild: c.NoRebuild, CheckLevel: bedCheckLevel(uf, bedNode)})
		if res != nil {
			fmt.Fprintf(os.Stderr, "charly check run %s: %s (steps=%d)\n",
				c.Name, summaryStatus(res.OK), len(res.Step))
		}
		// Propagate the check-fail exit code (2) when the bed failed at a check
		// step, so `charly check run <bed>` distinguishes "the thing under test
		// is broken" from an infra failure (build/deploy/vm-create) at exit 1.
		if runErr != nil && res != nil && res.FailExitCode == CheckFailExitCode {
			return &CheckFailedError{Msg: fmt.Sprintf("charly check run %s: checks failed", c.Name)}
		}
		return runErr
	}

	return c.runIterateEntity(uf, node, hasNode, cwd)
}

// runIterateEntity drives the iterate: AI iteration loop for the named entity:
// it resolves the sandbox target, generates a run ID, builds the run-local
// argv, performs the disposable-pod preflight, and dispatches to the host/pod/
// VM runner. Split out of Run, which keeps the kind:check bed-run branch inline.
func (c *CheckRunCmd) runIterateEntity(uf *UnifiedFile, node BundleNode, hasNode bool, cwd string) error {
	// iterate: path (AI iteration loop).
	if !hasNode || node.Iterate == nil {
		return fmt.Errorf("charly check run %s: no iterate: block and no kind:check bed by that name", c.Name)
	}
	tk, tn := ResolveIterateSandbox(uf, node.Iterate.Sandbox)

	runID := GenerateRunID()
	args := []string{"check", "run-local", c.Name, "--run-id", runID}
	if c.Agent != "" {
		args = append(args, "--agent", c.Agent)
	}
	if c.PlateauIteration > 0 {
		args = append(args, "--plateau-iteration", fmt.Sprintf("%d", c.PlateauIteration))
	}
	if c.MaxStep > 0 {
		args = append(args, "--max-step", fmt.Sprintf("%d", c.MaxStep))
	}
	if c.Tag != "" {
		args = append(args, "--tag", c.Tag)
	}
	if c.DryRun {
		args = append(args, "--dry-run")
	}
	if c.KeepRepo {
		args = append(args, "--keep-repo")
	}
	if c.SkipRebuild {
		args = append(args, "--skip-rebuild")
	}
	if c.Format != "" {
		args = append(args, "--format", c.Format)
	}

	// Per-run freshness for pod targets: if the harness sandbox is marked
	// disposable, restart its systemd quadlet unit so the container is
	// destroyed (`--rm` flag in the quadlet) and recreated from scratch
	// before dispatching. The sandbox pod IS the harness's sole disposable
	// resource — everything inside (deployments, images, AI work) is
	// the AI's job and lives inside the pod's nested podman, all
	// destroyed when the pod's container layer is wiped on restart.
	//
	// We deliberately do NOT go through a full redeploy here: that regenerates
	// the systemd quadlet via `charly config`, and the current generator
	// emits the image's default named volume AND the charly.yml bind
	// override at the same mount path (a pre-existing dedup bug). Going
	// through systemctl restart uses the existing on-disk quadlet
	// unchanged.
	if tk == TargetKindPod {
		cfg, err := LoadBundleConfig()
		if err != nil {
			return fmt.Errorf("loading per-host deploy overlay: %w", err)
		}
		entry, err := scorePodTargetEntry(cfg, c.Name, tn)
		if err != nil {
			return err
		}
		if entry.IsDisposable() {
			unit := "charly-" + tn + ".service"
			container := "charly-" + tn
			fmt.Fprintf(os.Stderr,
				"harness: preflight restart of disposable harness sandbox %q (fresh-per-run)\n", tn)
			cmd := exec.Command("systemctl", "--user", "restart", unit)
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("preflight restart of %s: %w", unit, err)
			}
			// The restart wipes the container's writable layer (the
			// `--rm` flag in the quadlet means restart = destroy +
			// recreate). Re-sync the host's fresh charly binary AND
			// claude credentials into the pod so the in-pod harness
			// has the latest code AND can authenticate. Without
			// these, the AI invocation fails with auth errors and
			// the in-pod charly falls back to the (older) image-baked
			// version which lacks post-cutover commands.
			ready := exec.Command("podman", "inspect", "--format", "{{.State.Running}}", container)
			_ = ready.Run()
			if exe, err := os.Executable(); err == nil && exe != "" {
				sync := exec.Command("podman", "cp", exe, container+":/usr/local/bin/charly")
				sync.Stdout = os.Stderr
				sync.Stderr = os.Stderr
				if err := sync.Run(); err != nil {
					return fmt.Errorf("preflight sync of charly binary into %s: %w", container, err)
				}
			}
			// Re-sync AI credentials (claude creds, etc.) into the
			// freshly-restarted pod. Use the host's just-built charly
			// binary so the sync logic itself is post-cutover.
			credSync := exec.Command(findCharlyForCheck(), "check", "sync-credential", c.Name)
			credSync.Stdout = os.Stderr
			credSync.Stderr = os.Stderr
			if err := credSync.Run(); err != nil {
				return fmt.Errorf("preflight sync of credentials for score %q: %w", c.Name, err)
			}
		}
	}

	switch tk {
	case TargetKindHost:
		// Test-bed image preflight. The deploy that prepared the host
		// installs candies (host packages + configs) only; container
		// images that plan steps spawn need to be pulled or built
		// before the score's runner walks them.
		if !c.DryRun {
			layers, _ := ScanCandy(cwd)
			plan, _ := ExpandPlanIncludes(uf, layers, node.Plan)
			if err := ensureScoreImages(context.Background(), plan, uf, cwd); err != nil {
				return err
			}
		}
		return runLocalInProcess(args, c.Name, runID, cwd)
	case TargetKindPod:
		return dispatchToPod(tn, c.Name, args)
	case TargetKindVM:
		return dispatchToVM(tn, c.Name, args)
	}
	return fmt.Errorf("unsupported target kind: %s", tk)
}

// scorePodTargetEntry resolves a score's pod-target deploy entry from the
// per-host overlay. The harness restarts-but-never-creates its sandbox pod
// (see the preflight comment in Run), so a missing entry is an operator
// precondition failure — fail fast with the remediation instead of letting
// podman surface a raw exec error against a container that cannot exist.
func scorePodTargetEntry(cfg *BundleConfig, scoreName, targetName string) (BundleNode, error) {
	if cfg != nil {
		if entry, ok := cfg.Bundle[targetName]; ok {
			return entry, nil
		}
	}
	return BundleNode{}, fmt.Errorf(
		"score %q targets pod %q but no deploy entry exists on this host — provision the harness sandbox first: `charly bundle add %s <ref> --disposable` then `charly start %s` (the sandbox is per-host operator config, never shipped by the repo; see /charly-check:check)",
		scoreName, targetName, targetName, targetName)
}

// runLocalInProcess invokes CheckRunLocalCmd in-process for host targets.
func runLocalInProcess(args []string, _, _ string, cwd string) error {
	exe, err := os.Executable()
	if err != nil {
		exe = "charly"
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = cwd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

func dispatchToPod(podName, scoreName string, args []string) error {
	containerName := "charly-" + podName
	full := append([]string{"exec", "-i", containerName, "charly"}, args...)
	cmd := exec.Command("podman", full...)
	if err := runWithPhaseResync(cmd, scoreName); err != nil {
		return fmt.Errorf("podman exec %s: %w", containerName, err)
	}
	return mirrorPodHarnessDir(containerName)
}

func mirrorPodHarnessDir(containerName string) error {
	cmd := exec.Command("podman", "cp", containerName+":/workspace/.check/.", "./.check/")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "harness: mirror artifacts back: %v (non-fatal)\n", err)
	}
	return nil
}

func dispatchToVM(vmName, scoreName string, args []string) error {
	full := append([]string{"vm", "ssh", vmName, "--", "charly"}, args...)
	cmd := exec.Command("charly", full...)
	return runWithPhaseResync(cmd, scoreName)
}

// checkPhaseRe matches the orchestrator's per-phase boundary marker:
//
//	harness: phase N/M — ...
//
// Captures phase number N. Progress lines have the form
// `harness: progress [phase N/M iter K] ...` and are deliberately not
// matched — only the boundary line should trigger a credential resync.
var checkPhaseRe = regexp.MustCompile(`^harness: phase (\d+)/\d+ —`)

// phaseResyncFn is the credential-resync hook invoked by
// runWithPhaseResync at every phase boundary (N >= 2). Default invokes
// `charly check sync-credential <score>` from the host. Tests override to
// record calls without spawning subprocesses.
var phaseResyncFn = func(scoreName string, phase int) error {
	cmd := exec.Command(findCharlyForCheck(), "check", "sync-credential", scoreName)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runWithPhaseResync runs cmd, forwarding stdout to os.Stdout, while
// watching stderr for the orchestrator's phase-boundary marker. On each
// new phase number N >= 2 it calls phaseResyncFn(scoreName, N) in a
// goroutine to refresh AI credentials on the target before iter 1's
// claude subprocess spawns.
//
// Why: the harness sandbox's `~/.claude/.credentials.json` is a one-shot copy
// taken by the host preflight. Anthropic OAuth access tokens are
// short-lived (typically ~8h, often less if the copy was already aged
// at run start). A long phase 4 — which can hold the AI for ~50 min on
// the watchdog — easily lets the in-pod token expire. After that, every
// claude spawn in subsequent phases bails with HTTP 401 and the run
// plateaus without ever exercising later phases.
//
// Phase 1 is intentionally skipped: CheckRunCmd.Run's preflight has
// already run `charly check sync-credential` immediately before dispatch, so
// phase 1's claude has the freshest possible credentials.
//
// The resync runs concurrently with the orchestrator's per-phase
// preflight (synthesize baseline, render scope, etc.) so the
// `podman cp` typically completes before iter 1's claude actually
// spawns. If the race goes the other way, only iter 1 of the affected
// phase fails authentication; the resync wins by iter 2 and plateau
// detection (plateau_iteration >= 2) keeps the phase alive.
func runWithPhaseResync(cmd *exec.Cmd, scoreName string) error {
	cmd.Stdout = os.Stdout
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	seen := map[int]bool{1: true} // preflight already covered phase 1
	scanner := bufio.NewScanner(stderrPipe)
	scanner.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintln(os.Stderr, line)
		m := checkPhaseRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		go func(phase int) {
			fmt.Fprintf(os.Stderr,
				"harness: phase %d boundary — resyncing AI credentials before iter 1\n",
				phase)
			if err := phaseResyncFn(scoreName, phase); err != nil {
				fmt.Fprintf(os.Stderr,
					"harness: credential resync at phase %d failed (continuing): %v\n",
					phase, err)
			}
		}(n)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "harness: stderr scan stopped early: %v\n", err)
	}
	return cmd.Wait()
}

// ---------------------------------------------------------------------------
// sync-credential — `charly check sync-credential <score>`
// ---------------------------------------------------------------------------

// CheckSyncCredCmd is `charly check sync-credential <score>`.
type CheckSyncCredCmd struct {
	Score string `arg:"" help:"Score name"`
	Agent string `name:"agent" help:"Sync credentials for this agent only (default: all configured)"`
}

func (c *CheckSyncCredCmd) Run() error { return c.RunActual() }

// ---------------------------------------------------------------------------
// AI-facing iteration helpers
// ---------------------------------------------------------------------------

// CheckScopeCmd reads the active iteration's scope.yml.
type CheckScopeCmd struct{}

func (c *CheckScopeCmd) Run() error {
	score := os.Getenv("CHARLY_EVAL_SCORE")
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	iter := os.Getenv("CHARLY_EVAL_ITERATION")
	if score == "" || runID == "" || iter == "" {
		return fmt.Errorf("charly check scope: must run inside an iteration (CHARLY_EVAL_SCORE/RUN_ID/ITERATION env required)")
	}
	cwd, _ := os.Getwd()
	path := fmt.Sprintf("%s/.check/%s/runs/%s/iter%s/scope.yml", cwd, score, runID, iter)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	_, err = os.Stdout.Write(data)
	return err
}

// CheckLastTagCmd prints the prior iteration's image tag.
type CheckLastTagCmd struct{}

func (c *CheckLastTagCmd) Run() error {
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	iter := os.Getenv("CHARLY_EVAL_ITERATION")
	if runID == "" || iter == "" {
		return fmt.Errorf("charly check last-test-tag: must run inside an iteration")
	}
	var k int
	_, _ = fmt.Sscanf(iter, "%d", &k) // best-effort: parse failure leaves k=0, caught by the k<=1 guard
	if k <= 1 {
		return fmt.Errorf("charly check last-test-tag: no prior iteration (k=%d)", k)
	}
	fmt.Printf("charlycheck-%s-iter%d\n", runID, k-1)
	return nil
}

// CheckSelfCheckCmd implements `charly check self-evaluate` — the AI's
// canonical self-verification path during a harness iteration.
//
// Behavior: invokes the SAME RunCheckLive function the
// end-of-iter harness scorer calls, against the SAME in-scope plan
// steps for the current phase, against the AI's live deployments.
// Output is a per-step verdict table (pass / fail / skipped).
// Exit 0 if every in-scope step passes, non-zero otherwise.
//
// Anti-deception property: reads `check.yml` from the project tree
// (cwd → LoadUnified), NOT from the per-iter repo clone the AI works in.
// Edits the AI might make to `.harness/<run>/repo/check.yml` do not change
// what self-evaluate evaluates — the same load path the harness scorer uses
// governs both. Every probe physically executes against the AI's live
// deployments (RunCheckLive re-runs each step), exactly like the harness scorer.
type CheckSelfCheckCmd struct{}

func (c *CheckSelfCheckCmd) Run() error {
	score := os.Getenv("CHARLY_EVAL_SCORE")
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	iter := os.Getenv("CHARLY_EVAL_ITERATION")
	phaseStr := os.Getenv("CHARLY_EVAL_PHASE")
	if score == "" || runID == "" || iter == "" {
		return fmt.Errorf("charly check self-evaluate: must run inside an iteration (CHARLY_EVAL_SCORE/RUN_ID/ITERATION env required)")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	// Load check.yml from the project tree, NOT from the per-iter
	// repo clone the AI works in. This is the load-bearing
	// anti-deception property: AI edits to the per-iter clone don't
	// change what self-evaluate sees, because the harness scorer at
	// iter end uses the same project-tree path.
	uf, ok, err := LoadUnified(cwd)
	if err != nil {
		return fmt.Errorf("charly check self-evaluate: load check.yml: %w", err)
	}
	if !ok || uf == nil {
		return fmt.Errorf("charly check self-evaluate: no check.yml at project root %s — self-evaluate must run from a directory with a project tree containing check.yml (typically /workspace inside the harness sandbox)", cwd)
	}
	node, found := uf.Bundle[score]
	if !found || node.Iterate == nil {
		return fmt.Errorf("charly check self-evaluate: entity %q has no iterate: block", score)
	}
	var phase int
	if phaseStr != "" {
		_, _ = fmt.Sscanf(phaseStr, "%d", &phase) // best-effort: parse failure leaves phase=0 (default phase)
	}

	layers, lerr := ScanCandy(cwd)
	if lerr != nil {
		return fmt.Errorf("charly check self-evaluate: scan candies: %w", lerr)
	}
	plan, err := ExpandPlanIncludes(uf, layers, node.Plan)
	if err != nil {
		return fmt.Errorf("charly check self-evaluate: expand includes: %w", err)
	}
	if len(plan) == 0 {
		fmt.Fprintln(os.Stdout, "charly check self-evaluate: empty plan")
		return nil
	}

	// Every probe re-runs against live systems, producing fresh artifacts.
	ctx := context.Background()
	live, err := RunCheckLive(ctx, score, score, plan)
	if err != nil {
		return fmt.Errorf("charly check self-evaluate: live scoring: %w", err)
	}

	// Print verdict table — same fields the orchestrator's scorer records into
	// result-<calver>.yml. The AI reads stdout to see pass/fail/skipped per step.
	fmt.Fprintf(os.Stdout, "self-evaluate: score=%s phase=%d iter=%s run=%s\n", score, phase, iter, runID)
	fmt.Fprintf(os.Stdout, "%-50s  %-7s  %s\n", "STEP", "STATUS", "DETAIL")
	failed := 0
	for _, st := range live.Step {
		detail := ""
		if st.SkippedReason != "" {
			detail = st.SkippedReason
		}
		fmt.Fprintf(os.Stdout, "%-50s  %-7s  %s\n", st.Text, st.Status, detail)
		if st.Status != "pass" && st.Status != "skip" {
			failed++
		}
	}
	fmt.Fprintf(os.Stdout, "summary: %d/%d pass, %d fail, %d skip (total %d)\n",
		live.Summary.Pass, live.Summary.Total, live.Summary.Fail, live.Summary.Skip, live.Summary.Total)
	if failed > 0 {
		return fmt.Errorf("self-evaluate: %d step(s) failed", failed)
	}
	return nil
}

// ---------------------------------------------------------------------------
// list / report — past-run inspection
// ---------------------------------------------------------------------------

// CheckListRunsCmd lists past runs across all scores.
type CheckListRunsCmd struct{}

func (c *CheckListRunsCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	runs, err := ListRuns(context.TODO(), cwd)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Println("No runs found under .check/.")
		return nil
	}
	fmt.Printf("%-20s  %-25s  %-10s  %s\n", "SCORE", "RUN_ID", "STATUS", "STARTED")
	for _, r := range runs {
		started := r.StartedUTC.Format("2006-01-02 15:04:05Z")
		fmt.Printf("%-20s  %-25s  %-10s  %s\n", r.Score, r.RunID, r.Status, started)
	}
	return nil
}

// CheckReportCmd prints a past result-<calver>.yml.
type CheckReportCmd struct {
	Score  string `arg:"" optional:"" help:"Score name (default: latest)"`
	Calver string `arg:"" optional:"" help:"Calver of the result to display (default: latest)"`
}

func (c *CheckReportCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	resultsRoot := fmt.Sprintf("%s/.harness", cwd)
	if c.Score == "" {
		entries, err := os.ReadDir(resultsRoot)
		if err != nil {
			return fmt.Errorf("no .harness directory in %s", cwd)
		}
		var newest os.DirEntry
		var newestT int64
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().Unix() > newestT {
				newestT = info.ModTime().Unix()
				newest = e
			}
		}
		if newest == nil {
			return fmt.Errorf("no scores under %s", resultsRoot)
		}
		c.Score = newest.Name()
	}
	resultsDir := fmt.Sprintf("%s/%s/results", resultsRoot, c.Score)
	if c.Calver == "" {
		entries, err := os.ReadDir(resultsDir)
		if err != nil {
			return fmt.Errorf("no results directory: %s", resultsDir)
		}
		var latest string
		for _, e := range entries {
			n := e.Name()
			if len(n) > 7 && n[:7] == "result-" && n[len(n)-4:] == ".yml" {
				if n > latest {
					latest = n
				}
			}
		}
		if latest == "" {
			return fmt.Errorf("no result files under %s", resultsDir)
		}
		c.Calver = latest[7 : len(latest)-4]
	}
	path := fmt.Sprintf("%s/result-%s.yml", resultsDir, c.Calver)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// ---------------------------------------------------------------------------
// note read / note append — persistent NOTES.md memory
// ---------------------------------------------------------------------------

// CheckNoteCmd groups the note read/append verbs.
type CheckNoteCmd struct {
	Read   CheckNoteReadCmd   `cmd:"" help:"Print the persistent NOTES.md for a score"`
	Append CheckNoteAppendCmd `cmd:"" help:"Atomically append a note to a score's NOTES.md"`
}

type CheckNoteReadCmd struct {
	Score string `arg:"" optional:"" help:"Score name (default: $CHARLY_EVAL_SCORE)"`
}

func (c *CheckNoteReadCmd) Run() error {
	score := c.Score
	if score == "" {
		score = os.Getenv("CHARLY_EVAL_SCORE")
	}
	if score == "" {
		return fmt.Errorf("charly check note read: score name required (pass as arg or set CHARLY_EVAL_SCORE)")
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	body, err := ReadNote(dir, score)
	if err != nil {
		return err
	}
	if body == "" {
		fmt.Fprintln(os.Stdout, "(empty)")
		return nil
	}
	fmt.Fprint(os.Stdout, body)
	return nil
}

type CheckNoteAppendCmd struct {
	Score string `arg:"" help:"Score name (or skip and pass --score)"`
	Text  string `arg:"" help:"Note text (one paragraph)"`
}

func (c *CheckNoteAppendCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	iter := os.Getenv("CHARLY_EVAL_ITERATION")
	ai := os.Getenv("CHARLY_EVAL_AGENT")
	if iter == "" {
		iter = "0"
	}
	return AppendNote(dir, c.Score, runID, iter, ai, c.Text)
}
