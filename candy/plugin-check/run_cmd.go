package check

// run_cmd.go — `charly check run <name>` host-side dispatcher (P12: relocated from
// charly/check_runner_cmd.go).
//
// Overloaded by the kind the name resolves to (classified via the "check-config"
// host seam): a check bed (a `disposable: true` deploy without an iterate: block)
// runs the full R10 sequence (runCheckBed → the "check-bed" session seam +
// HostBuild("cli")); an iterate: entity drives the AI iteration loop (dispatched into
// the sandbox target). ONE bed / one entity per invocation.

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"

	"github.com/opencharly/sdk/spec"
)

// Target-kind strings the "check-config" seam returns (ResolveIterateSandbox's
// result) — the plugin classifies the iterate sandbox by these instead of the core
// TargetKind type.
const (
	targetKindPod  = "pod"
	targetKindVM   = "vm"
	targetKindHost = "host"
)

// CheckRunCmd is `charly check run <name>` — a check bed runs the full R10 sequence
// (build → check box → deploy → check live → fresh update → tear down); an iterate:
// entity drives the AI iteration loop. The globally-unique node names keep a bed and
// an iterate entity disjoint.
type CheckRunCmd struct {
	Name  string `arg:"" optional:"" help:"disposable check bed (full R10 sequence) or iterate: entity (AI loop)."`
	Agent string `name:"agent" help:"Pick which agent to run (required if the entity's agent has more than one entry)"`

	// bed-path flags (ignored on the iterate: path).
	Keep      bool `name:"keep" help:"check beds: don't tear the bed down after the run"`
	NoRebuild bool `name:"no-rebuild" help:"check beds: skip the fresh-update R10 re-verify step (R10 acceptance gate)"`

	// Mutually-exclusive target overrides (iterate: path).
	Pod  string `name:"on-pod" xor:"target" help:"Override the iterate target with this pod deployment"`
	VM   string `name:"on-vm" xor:"target" help:"Override the iterate target with this VM"`
	Host bool   `name:"on-host" xor:"target" help:"Override the iterate target to run on the host directly"`

	PlateauIteration int    `name:"plateau-iteration" help:"Override plateau_iteration"`
	MaxStep          int    `name:"max-step" help:"Cap the pending input set"`
	Tag              string `name:"tag" help:"Override the tag expression"`
	DryRun           bool   `name:"dry-run" help:"Render scope+prompt without rebuild"`
	SkipRebuild      bool   `name:"skip-rebuild" help:"Source-only steps"`
	KeepRepo         bool   `name:"keep-repo" help:"Don't delete the per-run repo clone after the run (~100MB; debugging only)"`
	Format           string `name:"format" enum:"text,yaml" default:"text" help:"Output format"`
}

func (c *CheckRunCmd) Run() error {
	ex, ctx := cmdExec, cmdCtx
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	// Reusable-artifact retention: after the run completes (any path), trim .check to
	// defaults.keep_check_runs via the shared "retention" host seam (pruneCheckRuns
	// STAYS core — multi-caller). The host resolves the keep-count; the plugin prints
	// the pruned line. NOTES.md is always preserved; keep_check_runs 0/absent disables.
	defer func() {
		if reply, e := hostRetention(ex, ctx, spec.RetentionRequest{Check: true, Dir: cwd}); e == nil && len(reply.CheckPaths) > 0 {
			fmt.Fprintf(os.Stderr, "Pruned %d old check run artifact(s) (keep_check_runs=%d)\n", len(reply.CheckPaths), reply.KeepCheckRuns)
		}
	}()

	if c.Name == "" {
		// Run a whole roster by fanning beds out at the AGENT layer — one
		// `charly check run <bed>` per agent (the /verify-beds workflow / an agent team).
		return fmt.Errorf("charly check run: provide an iterate: entity or a check bed name (run a full roster concurrently via the /verify-beds workflow)")
	}

	reply, err := checkConfig(ex, ctx, spec.CheckConfigRequest{Entity: c.Name, Dir: cwd})
	if err != nil {
		return err
	}
	if !reply.HasNode && !reply.IsBed {
		return fmt.Errorf("charly check run: no entity %q in %s", c.Name, cwd)
	}

	// Dispatch: an entity carrying an iterate: block → the AI loop; a plain check bed
	// → the deterministic R10 sequence.
	if (!reply.HasNode || !reply.HasIterate) && reply.IsBed {
		res, runErr := runCheckBed(ctx, ex, c.Name, bedRunOpts{Keep: c.Keep, NoRebuild: c.NoRebuild})
		// A skipped bed (absent host prereq) is not a run — report SKIPPED and
		// propagate CheckSkippedExitCode (3).
		if res != nil && res.SkippedPrereq {
			return &CheckSkippedError{Msg: fmt.Sprintf("charly check run %s: skipped (%s)", c.Name, res.SkipReason)}
		}
		if res != nil {
			fmt.Fprintf(os.Stderr, "charly check run %s: %s (steps=%d)\n",
				c.Name, summaryStatus(res.OK), len(res.Step))
		}
		// Propagate the check-fail exit code (2) when the bed failed at a check step.
		if runErr != nil && res != nil && res.FailExitCode == CheckFailExitCode {
			return &CheckFailedError{Msg: fmt.Sprintf("charly check run %s: checks failed", c.Name)}
		}
		return runErr
	}

	return c.runIterateEntity(reply, cwd)
}

// runIterateEntity drives the iterate: AI iteration loop for the named entity: it
// resolves the sandbox target (from the "check-config" reply), generates a run ID,
// builds the run-local argv, performs the disposable-pod preflight, and dispatches to
// the host / pod / VM runner. The seams it drives (preflight check-run, cred sync,
// self-reentry) use the package cmdExec/cmdCtx, so no executor is threaded here.
func (c *CheckRunCmd) runIterateEntity(reply spec.CheckConfigReply, cwd string) error {
	if !reply.HasNode || !reply.HasIterate {
		return fmt.Errorf("charly check run %s: no iterate: block and no check bed by that name", c.Name)
	}
	tk, tn := reply.SandboxKind, reply.SandboxName

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

	// Per-run freshness for pod targets: if the harness sandbox is disposable,
	// restart its systemd quadlet so the container is destroyed (`--rm`) and recreated
	// before dispatch. The sandbox pod IS the harness's sole disposable resource;
	// everything inside is the AI's job and is wiped on restart. (PodTargetDisposable
	// is resolved host-side by the "check-config" seam.)
	if tk == targetKindPod && reply.PodTargetDisposable {
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
		// The restart wipes the container's writable layer. Re-sync the host's fresh
		// charly binary AND AI credentials into the pod.
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
		credSync := exec.Command(findCharlyForCheck(), "check", "sync-credential", c.Name)
		credSync.Stdout = os.Stderr
		credSync.Stderr = os.Stderr
		if err := credSync.Run(); err != nil {
			return fmt.Errorf("preflight sync of credentials for score %q: %w", c.Name, err)
		}
	}

	switch tk {
	case targetKindHost:
		// Test-bed image preflight (host target): the deploy that prepared the host
		// installs candies only; container images that plan steps spawn need pulling /
		// building first. Rides the "preflight" check-run mode (ensureScoreImages STAYS
		// core — the R3-shared EnsureImagePresent).
		if !c.DryRun {
			if _, err := hostCheckRun(spec.CheckRunRequest{Mode: "preflight", Name: c.Name, Dir: cwd}); err != nil {
				return err
			}
		}
		return runLocalInProcess(args, cwd)
	case targetKindPod:
		return dispatchToPod(tn, c.Name, args)
	case targetKindVM:
		return dispatchToVM(tn, c.Name, args)
	}
	return fmt.Errorf("unsupported target kind: %s", tk)
}

// runLocalInProcess invokes CheckRunLocalCmd as a self-reentry subprocess for host targets.
func runLocalInProcess(args []string, cwd string) error {
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
	// `vm ssh` addresses the guest by its managed alias charly-<domainIdentity>; the
	// iterate sandbox name IS the deploy name, so resolve its per-deploy domain
	// identity (a no-op for a plain name). A self-reentry subprocess (NOT the cli
	// seam) so runWithPhaseResync can scan the child stderr for phase boundaries.
	exe := findCharlyForCheck()
	full := append([]string{"vm", "ssh", vmDomainIdentity(vmName), "--", "charly"}, args...)
	cmd := exec.Command(exe, full...)
	return runWithPhaseResync(cmd, scoreName)
}

// checkPhaseRe matches the orchestrator's per-phase boundary marker:
//
//	harness: phase N/M — ...
//
// Captures phase number N. Progress lines (`harness: progress [phase N/M iter K]`) are
// deliberately not matched — only the boundary line triggers a credential resync.
var checkPhaseRe = regexp.MustCompile(`^harness: phase (\d+)/\d+ —`)

// phaseResyncFn is the credential-resync hook invoked by runWithPhaseResync at every
// phase boundary (N >= 2). Default spawns `charly check sync-credential <score>`.
var phaseResyncFn = func(scoreName string, phase int) error {
	cmd := exec.Command(findCharlyForCheck(), "check", "sync-credential", scoreName)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runWithPhaseResync runs cmd, forwarding stdout, while watching stderr for the
// orchestrator's phase-boundary marker. On each new phase number N >= 2 it calls
// phaseResyncFn(scoreName, N) in a goroutine to refresh AI credentials before iter 1
// (Anthropic OAuth tokens are short-lived; a long phase can expire the in-pod token).
// Phase 1 is skipped: the preflight already synced.
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
	// resyncWG tracks per-boundary resync goroutines so the function WAITS for them
	// before returning: each reads phaseResyncFn (a package-var test seam), so leaking
	// one past the return would race + leave a credential sync mid-flight. wg.Wait()
	// establishes the happens-before — a synchronization primitive, never a sleep.
	var resyncWG sync.WaitGroup
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
		resyncWG.Add(1)
		go func(phase int) {
			defer resyncWG.Done()
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
	waitErr := cmd.Wait()
	resyncWG.Wait() // no resync goroutine (nor its phaseResyncFn read) outlives the return
	return waitErr
}
