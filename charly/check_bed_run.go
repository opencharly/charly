package main

// check_bed_run.go — the R10 acceptance-sequence engine for disposable
// test beds, driven by `charly check run <bed>` (fan a roster out via /verify-beds).
//
// A check bed is a `disposable: true` BundleNode in the Bundle map (CheckBeds()
// derives the bed set from the disposable bundles) describing a disposable target.
// runCheckBed drives the canonical sequence against it:
//
//	build → check box → deploy add → config → start → check live →
//	fresh update (R10 acceptance gate) → tear down
//
// Every parameter is read from the bed's BundleNode — there is NO
// hardcoded bed table. The target kind selects the bring-up/tear-down path:
//
//	target: pod   → charly box build + charly check box + charly bundle add +
//	                charly config + charly start + charly check live
//	target: vm    → charly vm build + charly vm create + charly bundle add + charly check
//	                live (image build / check box skipped — the substrate
//	                is a cloud_image, not an charly box)
//	target: local → charly bundle add only (kind:local applies candies in place;
//	                no container/VM to exec check live against)
//
// The dispatcher SHELLS OUT to the same `charly` binary the caller invoked, so
// each verb keeps its own validation, error reporting, and side effects —
// no probe/build/deploy logic is re-implemented here.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

// bedVmDomains returns the sorted, deduped libvirt domain names (charly-<from>) a bed's
// VM(s) occupy — the bed's own vm target plus any group-member vm targets. This is the
// unit of exclusive host contention two DISTINCT beds can collide on (the per-domain lock
// in runCheckBed serializes them).
func bedVmDomains(node BundleNode) []string {
	seen := map[string]bool{}
	var out []string
	add := func(tmpl string) {
		if tmpl == "" || seen[tmpl] {
			return
		}
		seen[tmpl] = true
		out = append(out, "charly-"+tmpl)
	}
	if node.Target == "vm" {
		add(node.From)
	}
	for _, m := range node.Members {
		if isVmMember(m) {
			add(m.From)
		}
	}
	sort.Strings(out)
	return out
}

// acquireVmDomainLock takes a BLOCKING, host-global advisory lock serializing every check
// bed that occupies the given libvirt domain. Host-global (under ~/.cache/charly/.locks/)
// because the qemu:///session domain namespace is host-wide, shared across project dirs.
func acquireVmDomainLock(domain string) (func() error, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".cache", "charly", ".locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return acquireFileLock(filepath.Join(dir, "vm-domain-"+domain+".lock"), true)
}

// bedRunOpts carries the per-run knobs (sourced from `charly check run` flags).
type bedRunOpts struct {
	Keep       bool   // don't tear the bed down after the run (--keep)
	NoRebuild  bool   // skip the fresh-update R10 re-verify step (--no-rebuild)
	CheckLevel string // the bed box's acceptance-depth rung (none|build|noagent|agent); gates how deep the run drives acceptance. Empty → DefaultCheckLevel.
}

// bedCheckLevel resolves the acceptance-depth rung for a bed from its box's
// authored check_level (none → DefaultCheckLevel). VM / local beds carry no box
// image, so they always run at the default rung.
func bedCheckLevel(uf *UnifiedFile, node BundleNode) string {
	if node.Image == "" {
		return DefaultCheckLevel
	}
	if bc, _, ok := uf.ProjectConfig().resolveBoxRef(node.Image); ok {
		return ResolveCheckLevel(bc.CheckLevel)
	}
	return DefaultCheckLevel
}

// stepResult captures one step's outcome for the summary.yml.
type stepResult struct {
	Name     string
	Duration time.Duration
	OK       bool
}

// bedRunResult captures one bed's full run outcome.
type bedRunResult struct {
	Bed    string
	CalVer string
	Step   []stepResult
	OK     bool
	// FailExitCode is the exit code of the FIRST failed step (0 = none).
	// CheckFailExitCode (2) means an check step (check-image/check-live)
	// reported failing checks; anything else is an infra failure (build /
	// deploy / vm-create). The caller maps it to the process exit code so
	// `charly check run <bed>` distinguishes "checks failed" from "couldn't run".
	FailExitCode int
	// SkippedPrereq marks a bed that never ran because a required HOST
	// prerequisite is absent (a GPU vendor with no matching card). Not a
	// failure — the caller emits CheckSkippedExitCode + SkipReason. OK stays
	// true (nothing failed), so callers MUST check SkippedPrereq before OK.
	SkippedPrereq bool
	SkipReason    string
}

// exitCodeOf extracts a subprocess exit code from an exec error. Non-ExitError
// failures (couldn't even spawn) map to 1; nil maps to 0.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	return 1
}

// summaryStatus formats a bool as a human-readable status word.
func summaryStatus(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// bedExternalInPlace reports whether a bed ROOT's substrate is an EXTERNAL deploy substrate
// that applies its workload IN PLACE — local-like: NO container image to build, NO `charly
// config`/`charly start`, teardown via `charly bundle del` (replay the recorded reverse
// ops). local/android/k8s/exampledeploy are in-place (they carry no `image:`).
//
// pod is the ONE externalized substrate that is NOT in-place: it builds + runs a container
// image and keeps the FULL pod lifecycle (image build → config → start → check-live →
// `charly remove`), so the bed runner must drive it through the DEFAULT pod
// path exactly as the in-proc pod — only the `charly bundle add` overlay build internally
// routes through pod's external deploy target + lifecycle hook now (invisible to the bed
// runner). Excluding pod here is consistent with the bed runner's other substrate-identity
// checks (isVM = target=="vm", isLocal = target=="local"); vm sidesteps the in-place logic
// via its own `case isVM` branch, so this exclusion is pod's analogue.
func bedExternalInPlace(target string) bool {
	return isExternalDeploySubstrate(target) && target != "pod"
}

// printDebugRetentionNotice tells the operator that a FAILED bed was left
// running for inspection, with the target-appropriate inspect + destroy
// commands. Pod/local beds tear down with `charly remove`; VM beds with
// `charly vm destroy`. The next `charly check run` best-effort clears the lingering
// target before rebuilding, so leaving it up never blocks a re-run.
func printDebugRetentionNotice(w io.Writer, name string, node BundleNode) {
	// The bed ran with CHARLY_REPO_OVERRIDE set (testing the LOCAL checkout's
	// candies + plugins), so a BARE `charly check live` would resolve the
	// @main-published candies/plugins instead — a DIFFERENT deployment whose
	// plugins are built from the @main cache, not this tree. Carry the same
	// override (still active here — set before the run, restored on return) so
	// the inspect command reproduces the bed's actual state. See selfSuperproject
	// OverridePair / GitClone's sdk-submodule population.
	live := "charly check live " + name
	if ov := os.Getenv(RepoOverrideEnv); ov != "" {
		live = RepoOverrideEnv + "='" + ov + "' " + live
	}
	switch {
	case node.Target == "vm":
		fmt.Fprintf(w, "\n[charly check run] bed %q FAILED — VM %q left running for debugging.\n"+
			"  inspect: %s | charly vm ssh %s\n"+
			"  destroy: charly vm destroy %s\n", name, node.From, live, node.From, node.From)
	case node.Target == "local":
		fmt.Fprintf(w, "\n[charly check run] bed %q FAILED — local apply left in place for debugging.\n"+
			"  destroy: charly remove %s\n", name, name)
	case bedExternalInPlace(node.Target):
		fmt.Fprintf(w, "\n[charly check run] bed %q FAILED — external deploy apply left in place for debugging.\n"+
			"  destroy: charly bundle del %s\n", name, name)
	default: // pod
		fmt.Fprintf(w, "\n[charly check run] bed %q FAILED — pod left running for debugging.\n"+
			"  inspect: %s | podman exec charly-%s sh\n"+
			"  destroy: charly remove %s\n", name, live, name, name)
	}
}

// persistBedDeployOverrides seeds the per-host charly.yml with a kind:check
// bed's project-declared deploy-shaped fields (port / volume / env / tunnel /
// security / network), its disposable/lifecycle classification, AND its
// resource-arbitration role (preemptible holder / requires_exclusive /
// requires_shared claimant), BEFORE the bed's `charly config` step runs. Seeding
// the arbiter fields is what lets a bed/deploy MEMBER be an arbiter participant:
// bringUpMembers persists each member here, then the member's `charly start`
// reloads the per-host node and fires the arbiter off these fields (start.go →
// acquireResourceForClaimant; preempt.go's holder gather) — without them a
// member's requires_exclusive reloaded as [] and the arbiter silently no-op'd.
// The folded bed node is the source of truth, but
// `charly bundle add` / `charly config` otherwise source those fields from the IMAGE
// LABELS and gate port writes behind an operator `-p` — so a bed's declared
// `port:` remap would never reach the quadlet (it would fall back to the image
// default and collide with any same-image deploy already bound to that port).
// Seeding the per-host entry up front lets the existing
// MergeDeployOntoMetadata → quadlet path honor the overrides with no new merge
// logic; `charly config`'s own SetPorts-gated save then leaves the seeded port
// untouched (it passes no `-p`). saveDeployState's per-field guards make
// unset bed fields no-ops, so this is safe for beds that declare only a subset.
func persistBedDeployOverrides(name string, node BundleNode) {
	// A GROUP bed (boxless root + sibling Members — the §3 cross-deployment
	// shape) has NO root deployment to seed: its members each carry their own
	// port/volume/env overrides (bringUpMembers persists every member), and the
	// boxless root is never `charly config`'d. Persisting the group root here would
	// write a MEMBERLESS bed (no box, no members — saveDeployState carries no
	// member fields) that validateCheckBeds then HARD-REJECTS on the next overlay
	// load ("no workload cross-ref and no sibling members"), poisoning every
	// subsequent saveDeployState. So never persist a group bed root.
	if node.IsGroup() {
		return
	}
	// A LOCAL or EXTERNAL in-place bed never runs `charly config` (it applies candies
	// in place during `charly bundle add` — see runCheckBed's `if !isInPlace` config
	// guard), so the whole reason persistBedDeployOverrides exists — seeding
	// port/volume/env overrides BEFORE config — does not apply. Worse, a local bed's
	// only persistable
	// cross-ref is its `local:` template, which lives in the bed's OWN project; writing
	// it into the GLOBAL per-host overlay makes that overlay un-loadable from every
	// OTHER project (validateCheckBeds: "references local template … which is not
	// defined"), poisoning concurrent/cross-project bed runs. Local deploys persist via
	// the install ledger, not this bundle-map path, so skipping is also lossless.
	if node.Target == "local" || bedExternalInPlace(node.Target) {
		return
	}
	saveDeployState(name, "", SaveDeployStateInput{
		Ports:         node.Port,
		SetPorts:      len(node.Port) > 0,
		Volume:        node.Volume,
		Env:           node.Env,
		CleanEnv:      true,
		Tunnel:        node.Tunnel,
		Security:      node.Security,
		Network:       node.Network,
		Box:           node.Image,
		Target:        node.Target,
		SetDisposable: true,
		Disposable:    node.IsDisposable(),
		SetLifecycle:  node.Lifecycle != "",
		Lifecycle:     node.Lifecycle,
		// Resource-arbitration role — so a group MEMBER (holder / claimant) can
		// actually drive the arbiter after its `charly start` reloads this entry.
		Preemptible:       node.Preemptible,
		RequiresExclusive: node.RequiresExclusive,
		RequiresShared:    node.RequiresShared,
	})
}

// runCheckBed executes the canonical R10 sequence for one check bed (a `disposable: true` bundle)
// and writes per-step logs + summary.yml to .check/<name>/<calver>/. Returns
// the result struct (always non-nil) and the first error encountered.
//
//nolint:gocyclo // canonical R10 bed sequence (build→check→deploy→check-live→update→teardown) woven from 6 interdependent inline closures (step/stepReady/fail/checkLiveTree/recoverVMIfDown/cleanup) over a shared mutable deployed flag + defer-bound preempt lease; contiguous-block extraction is not behavior-preserving
func runCheckBed(exe, name string, node BundleNode, opts bedRunOpts) (*bedRunResult, error) {
	isVM := node.Target == "vm"
	isLocal := node.Target == "local"
	// An IN-PLACE EXTERNAL deploy substrate (e.g. `exampledeploy`/android/k8s/local,
	// served by an out-of-process deploy plugin): it applies in place on the host venue via
	// the E3b reverse channel — like a kind:local deploy it has no image to build, runs no
	// `charly config`/`charly start`, and tears down via `charly bundle del` (which replays
	// the recorded reverse op). isInPlace unifies local + in-place-external at every "apply
	// candies in place, no container lifecycle" decision below. pod is EXCLUDED
	// (bedExternalInPlace) even though its substrate is externalized: it builds + runs a
	// container image, so it stays on the DEFAULT pod path (build → config → start → remove)
	// exactly as the in-proc pod — only `bundle add` routes through pod's external machinery.
	isExternalDeploy := bedExternalInPlace(node.Target)
	isInPlace := isLocal || isExternalDeploy
	// A GROUP bed (the §3 cross-deployment shape): no root box/vm/local cross-ref,
	// but sibling Members (subject + driver) that ARE the deployment. It has no
	// root container, so its R10 sequence builds/deploys/rebuilds the MEMBERS
	// (via bringUpMembers) and check-lives the flattened venue-stamped plan
	// (runGroupCheck), instead of the workload-root build→deploy→update path.
	isGroup := node.IsGroup()
	image := node.Image
	vmTemplate := node.From
	localRef := node.From

	// Acceptance-depth gating by the box's check_level rung (see check_level.go):
	//   none    → neither build- nor runtime-acceptance (build+deploy smoke only)
	//   build   → build-context acceptance (check box) only
	//   noagent → + deploy/runtime acceptance (check live + feature run --no-agent)
	//   agent   → + the prose-step agent grader (feature run WITHOUT --no-agent)
	// The default (noagent) preserves the prior full-sequence behavior.
	level := ResolveCheckLevel(opts.CheckLevel)
	runBuildCheck := CheckLevelReaches(level, CheckLevelBuild)
	runRuntimeCheck := CheckLevelReaches(level, CheckLevelNoAgent)
	runAgentGrader := CheckLevelReaches(level, CheckLevelAgent)
	featureRunArgs := func() []string {
		args := []string{"check", "feature", "run", name}
		if !runAgentGrader {
			args = append(args, "--no-agent")
		}
		return args
	}

	calver := ComputeCalVer()
	logDir := filepath.Join(".check", name, calver)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", logDir, err)
	}
	res := &bedRunResult{Bed: name, CalVer: calver, OK: true}

	// Host-prerequisite fail-fast (BEFORE any build/acquire): a bed claiming a
	// GPU resource whose vendor has no matching card on this host is
	// unsatisfiable here — building a 10-30 GB image only to die at start with
	// "unresolvable CDI devices nvidia.com/gpu=all" is waste. Skip it cleanly
	// (like a missing /dev/kvm), deterministically, so automation records a SKIP
	// not a FAIL. See bedGPUPrereqMissing.
	if tok, vendor, missing := bedGPUPrereqMissing(node); missing {
		res.SkippedPrereq = true
		res.SkipReason = fmt.Sprintf("no GPU matching vendor %s on this host (bed requires resource %q)", vendor, tok)
		res.Step = append(res.Step, stepResult{Name: "prereq-gpu-skipped", OK: true})
		writeBedSummary(logDir, res)
		fmt.Fprintf(os.Stderr, "charly check run %s: SKIPPED — %s\n", name, res.SkipReason)
		return res, &CheckSkippedError{Msg: fmt.Sprintf("charly check run %s: skipped (%s)", name, res.SkipReason)}
	}

	// Per-bed exclusive lock — refuse a SECOND concurrent `charly check run` of
	// the SAME bed in this project. Without it the second run's pre-run
	// destroy-first (`charly remove <name> --purge` / `charly vm destroy` below)
	// would wipe the first run's LIVE target mid-test, and the two would collide
	// on the `charly-<name>` container name and the `.check/<name>/` dir. Fail-
	// fast (non-blocking): a duplicate same-bed launch is a fan-out mistake to
	// surface, not to silently serialize. DISTINCT beds (and distinct project
	// dirs) take DISTINCT locks, so they still run fully in parallel — only the
	// shared deploy-config read-modify-write (acquireDeployConfigLock) serializes
	// them, and only briefly. Held through teardown via defer.
	bedUnlock, bedLockErr := acquireFileLock(filepath.Join(".check", name, ".lock"), false)
	if bedLockErr != nil {
		res.OK = false
		res.Step = append(res.Step, stepResult{Name: "bed-lock", OK: false})
		writeBedSummary(logDir, res)
		if errors.Is(bedLockErr, errLockBusy) {
			return res, fmt.Errorf("check bed %q is already running in this project — refusing a concurrent run (lock: .check/%s/.lock)", name, name)
		}
		return res, fmt.Errorf("locking check bed %q: %w", name, bedLockErr)
	}
	defer func() { _ = bedUnlock() }()

	// Per-DOMAIN serialization for VM beds. A disposable bed's VM(s) occupy libvirt
	// domain(s) charly-<from>; two DISTINCT beds deploying the SAME vm template (a direct
	// `from: k3s-vm` bed and a group member `from: k3s-vm`) collide on ONE domain, so a
	// naive parallel roster clobbers one mid-boot (the wait-for-sshd / define-UUID-collision
	// incident). Take a BLOCKING, HOST-GLOBAL lock per domain (sorted → no deadlock across a
	// multi-domain bed), held through teardown, so same-domain beds SERIALIZE (both pass)
	// while distinct-domain beds stay fully parallel — the concurrency-mandate's
	// per-exclusive-resource SERIAL group, auto-keyed by the domain. Host-global because the
	// libvirt session is host-wide (shared across project dirs), not per-.check/.
	for _, domain := range bedVmDomains(node) {
		domUnlock, domErr := acquireVmDomainLock(domain)
		if domErr != nil {
			res.OK = false
			res.Step = append(res.Step, stepResult{Name: "vm-domain-lock", OK: false})
			writeBedSummary(logDir, res)
			return res, fmt.Errorf("locking vm domain %s for bed %q: %w", domain, name, domErr)
		}
		defer func() { _ = domUnlock() }()
	}

	// Local-candy resolution (the candy-ref analogue of --dev-local-pkg): a bed
	// in a box/<distro> submodule pulls its parent repo's shared candies via
	// `@github.com/<org>/<parent>/candy/...:<tag>` refs, which would otherwise
	// fetch the PINNED REMOTE candy — so the bed would test STALE code, not the
	// in-development tree (CLAUDE.md R10/RDD: a bed must verify code-under-
	// development). Auto-point those refs at the local superproject working tree
	// (the `:vTAG` is ignored) so EVERY bed tests the latest local candies. An
	// explicit operator CHARLY_REPO_OVERRIDE entry for the same repo still wins
	// (mergeRepoOverrides places it first). Scoped + restored so concurrent bed runs and
	// the test suite don't leak the env.
	if pair := selfSuperprojectOverridePair("."); pair != "" {
		old, had := os.LookupEnv(RepoOverrideEnv)
		_ = os.Setenv(RepoOverrideEnv, mergeRepoOverrides(old, pair))
		defer func() {
			if had {
				_ = os.Setenv(RepoOverrideEnv, old)
			} else {
				_ = os.Unsetenv(RepoOverrideEnv)
			}
		}()
		fmt.Fprintf(os.Stderr, "charly check run %s: testing LOCAL candies (%s += %s)\n", name, RepoOverrideEnv, pair)
	}

	// Isolate this bed's EPHEMERAL deploy state (resolved_port, quadlet entries) to a
	// PER-BED config file so CONCURRENT beds never share — and corrupt — the operator's
	// ~/.config/charly/charly.yml. A disposable bed's transient deploy state must not
	// touch the operator's persistent config, and the shared read-modify-write of that
	// one file across N concurrent bed processes is what produced the maxjobs-load
	// corruption (`kind:group: #GroupInput.resolved_port: field not allowed`). The bed's
	// definition still comes from the PROJECT charly.yml (via CHARLY_REPO_OVERRIDE); only
	// the per-host deploy OVERLAY is isolated. Env-scoped so the config subprocesses this
	// run spawns (charly config/start/update) inherit it; restored + cleaned on return.
	if _, already := os.LookupEnv(DeployConfigEnv); !already {
		if bedCfgDir, mkErr := os.MkdirTemp("", "charly-bed-cfg-"+name+"-"); mkErr == nil {
			_ = os.Setenv(DeployConfigEnv, filepath.Join(bedCfgDir, "charly.yml"))
			defer func() {
				_ = os.Unsetenv(DeployConfigEnv)
				_ = os.RemoveAll(bedCfgDir)
			}()
		}
	}

	// Resource arbitration (the "preemptible" axis): if this bed claims a host
	// resource — EXCLUSIVE (requires_exclusive — sole use, e.g. a passthrough
	// GPU VM) or SHARED (requires_shared — refcounted, e.g. a GPU shared across
	// pods via CDI) — the arbiter frees/flips it BEFORE bring-up (stopping any
	// running preemptible holder; for shared, flipping the GPU to nvidia + CDI)
	// and restores it AFTER teardown. The lease is owned HERE (the outermost
	// orchestrator) and CHARLY_PREEMPT_LEASE suppresses the nested `charly vm create`/
	// `charly bundle add`/`charly vm destroy` subprocesses from touching it. The defer
	// guarantees restore on EVERY exit path (success, failure, early return);
	// crash-recovery beyond the defer is handled by the ledger + `charly preempt
	// restore`. See charly/preempt.go.
	lease, lerr := acquireResourceForClaimant(name, node, true)
	if lerr != nil {
		res.OK = false
		res.Step = append(res.Step, stepResult{Name: "preempt-acquire", OK: false})
		writeBedSummary(logDir, res)
		return res, fmt.Errorf("acquiring resources for %s: %w", name, lerr)
	}
	defer func() {
		if res.OK {
			_ = lease.Release()
		} else {
			_ = lease.ReleaseFailed()
		}
		if lease.active {
			_ = os.Unsetenv(envPreemptLeaseHeld)
		}
	}()

	// step records a step's outcome and writes its log file. Returns the
	// run error so the caller can short-circuit via fail().
	step := func(stepName string, args []string) error {
		t0 := time.Now()
		out, runErr := runCapture(exe, args)
		dur := time.Since(t0)
		ok := runErr == nil
		res.Step = append(res.Step, stepResult{Name: stepName, Duration: dur, OK: ok})
		if !ok {
			res.OK = false
			if res.FailExitCode == 0 {
				// First failure wins (fail() short-circuits the sequence);
				// capture the sub-charly exit code so the caller can tell an
				// check-check failure (2) from an infra failure (1).
				res.FailExitCode = exitCodeOf(runErr)
			}
		}
		// Write the step log even on success — useful for debugging
		// non-fatal warnings.
		logPath := filepath.Join(logDir, stepName+".log")
		if writeErr := os.WriteFile(logPath, out, 0o644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "charly check run %s: writing %s: %v\n", name, logPath, writeErr)
		}
		return runErr
	}

	// stepReady runs a step with a bounded readiness retry: it re-runs the
	// command until it succeeds or the deadline elapses, recording only the
	// FINAL attempt. A service with slow first-run startup (e.g. a fresh Immich
	// running its one-shot DB migration before the API binds) is not ready when
	// the container is merely exec-able, so the deploy-scope check probes need a
	// readiness poll — the checks THEMSELVES are the readiness condition (a real
	// synchronization primitive, not a fixed sleep). Fast beds pass on the first
	// attempt (zero added latency); a genuinely-broken deploy still fails after
	// the deadline.
	stepReady := func(stepName string, args []string, beforeRetry func()) error {
		t0 := time.Now()
		var out []byte
		var lastErr error
		// CAP-ONLY readiness via the unified pollUntil primitive (poll.go): each
		// tick runs a full `charly check live` pass. The pass tally is too
		// coarse + format-fragile to be a reliable no-progress marker (a single
		// long migration flips zero checks), so we wait on a GENEROUS,
		// config-sourced cap — replacing the fixed 6m magic deadline that was too
		// short for a slow-but-progressing deploy under heavy parallel load.
		// recoverVMIfDown folds in as the per-attempt recovery. PollHeavy's
		// per-attempt (PerAttemptHeavy, ~15m) bounds the WHOLE multi-probe pass
		// generously — NOT the 120s single-probe bound that used to guillotine a
		// slow-but-progressing pass mid-flight under load; the never-hang for an
		// individual wedged probe now lives INSIDE check-live (Runner.probeNeverHang),
		// so a stuck probe is cancelled individually and the pass still completes
		// (which also means the step log is written in full, never empty-on-SIGKILL).
		// Fast beds pass on the first tick (zero added latency); a genuinely-broken
		// deploy fails at the cap, surfacing the LAST check-live error (not the cap sentinel).
		cfg := loadedReadiness().WaitCapped(stepName, PollHeavy, 0)
		pollErr := pollUntil(context.Background(), cfg, func(actx context.Context) (bool, float64, error) {
			out, lastErr = runCaptureCtx(actx, exe, args)
			if lastErr == nil {
				return true, 0, nil
			}
			if beforeRetry != nil {
				beforeRetry()
			}
			return false, 0, nil
		})
		ok := pollErr == nil
		res.Step = append(res.Step, stepResult{Name: stepName, Duration: time.Since(t0), OK: ok})
		if !ok {
			res.OK = false
			if res.FailExitCode == 0 {
				res.FailExitCode = exitCodeOf(lastErr)
			}
		}
		logPath := filepath.Join(logDir, stepName+".log")
		if writeErr := os.WriteFile(logPath, out, 0o644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "charly check run %s: writing %s: %v\n", name, logPath, writeErr)
		}
		return lastErr
	}

	// recoverVMIfDown is the check-live retry recovery hook for a disposable VM
	// bed: if the guest became unreachable mid-check (e.g. a rare host-side
	// QEMU/spice-server crash on a probe connect — see the 2026-05 RCA), restart
	// the domain and wait for sshd so the NEXT check-live retry runs against a
	// LIVE guest instead of pointlessly re-failing against a dead one. A
	// detect→restart→wait-ready recovery primitive, NOT a blind sleep-retry: it
	// no-ops when the guest still answers (the check-live failure is then a real
	// check failure to surface) and for non-VM / non-disposable beds.
	recoverVMIfDown := func() {
		if !isVM || !node.IsDisposable() {
			return
		}
		alias := "charly-" + vmTemplate
		probe := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=3",
			"-o", "LogLevel=ERROR", alias, "true")
		if probe.Run() == nil {
			return // guest answers — not a dead VM
		}
		fmt.Fprintf(os.Stderr, "charly check run %s: VM bed %q unreachable mid-check — restarting disposable domain before retry\n", name, vmTemplate)
		_ = exec.Command(exe, "vm", "start", vmTemplate).Run()
		waitForVmSshReady(vmTemplate)
	}

	// cleanup tears the disposable bed down (suppressed by --keep). Used on
	// both the happy-path tear-down AND the failure path so the bed doesn't
	// linger after a partial run.
	cleanup := func() {
		if opts.Keep {
			return
		}
		switch {
		case isVM:
			_ = step("cleanup", []string{"vm", "destroy", vmTemplate})
		case isExternalDeploy:
			// External deploy: `bundle del` replays the recorded reverse op (e.g.
			// removes the apply markers); `charly remove` is pod-quadlet-specific.
			_ = step("cleanup", []string{"bundle", "del", name})
		default:
			// --purge removes the bed's named volumes too. Safe because a
			// disposable bed's volumes are re-scoped to its own deploy key
			// (charly-<bed>-<vol>), never a production deploy's charly-<image>-<vol>.
			_ = step("cleanup", []string{"remove", name, "--purge"})
		}
		// Tear down any sibling peer deployments (companion driver pods) the
		// bed brought up alongside its root. Best-effort; never blocks teardown.
		tearDownMembers(&node)
	}

	// fail is the SINGLE failure tail shared by every step: record the
	// summary, wrap the error, and — crucially — LEAVE THE BED RUNNING so
	// the operator can debug the live target (the check-live failure is
	// already on record). Teardown on failure is deliberately suppressed:
	// the happy path (and --keep) still controls teardown via cleanup() at
	// the end, but a FAILED run preserves the target for inspection
	// (`charly check live <name>`, `podman exec charly-<name> …`, `charly check adb/appium
	// <name> …`). The next `charly check run` best-effort removes the lingering
	// bed before rebuilding (see the pre-run cleanup below), so kept-alive
	// state never blocks a re-run.
	// deployed flips true once the bed's target actually exists (after
	// deploy-add). The debug-retention notice is gated on it: a failure at
	// image-build / check-image (before any target is created) has nothing to
	// keep running, so it must NOT claim a pod was left up.
	deployed := false
	fail := func(format string, args ...any) (*bedRunResult, error) {
		writeBedSummary(logDir, res)
		if deployed {
			printDebugRetentionNotice(os.Stderr, name, node)
		}
		return res, fmt.Errorf(format, args...)
	}

	// Steps 1+2: image build + check box (pod beds only; VM substrate is a
	// cloud_image and kind:local has no image to build/disposable-check).
	if !isVM && !isLocal && image != "" {
		// Disposable check beds ALWAYS bake the IN-DEVELOPMENT charly toolchain
		// (any localpkg candy built from local source), never a stale published
		// release — so a bed tests the code under development. --dev-local-pkg is
		// the generic switch; production box builds omit it (download the release).
		if err := step("image-build", []string{"box", "build", image, "--dev-local-pkg"}); err != nil {
			return fail("image build %s: %w", image, err)
		}
		// Build-context acceptance — gated out at check_level: none.
		if runBuildCheck {
			if err := step("check-image", []string{"check", "box", image}); err != nil {
				return fail("check box %s: %w", image, err)
			}
		}
	}

	// Group bed: no root image — build EACH member's substrate (the same set
	// bringUpMembers brings up). A pod member builds its box image; a VM member
	// builds its disk (vm build) so bringUpMembers' vm create has a disk to boot;
	// a k8s member (an external-substrate workload, e.g. the check-k8s-deploy bed's
	// box) builds its box image so the deploy preresolver's ExtractMetadata finds it
	// locally; a kind:local member carries no image to build (it applies candies in
	// place).
	if isGroup {
		for _, memberKey := range sortedMemberKeys(node.Members) {
			m := node.Members[memberKey]
			if m == nil {
				continue
			}
			if isVmMember(m) {
				// VM member: build its disk here; bringUpMembers does vm create + ssh-wait.
				startLibvirtUserSession()
				if err := step("vm-build-"+memberKey, []string{"vm", "build", m.From}); err != nil {
					return fail("vm build member %s (%s): %w", memberKey, m.From, err)
				}
				continue
			}
			if m.Image == "" {
				continue // kind:local member — applies candies in place, no image
			}
			if err := step("image-build-"+memberKey, []string{"box", "build", m.Image, "--dev-local-pkg"}); err != nil {
				return fail("image build member %s (%s): %w", memberKey, m.Image, err)
			}
			if runBuildCheck {
				if err := step("check-image-"+memberKey, []string{"check", "box", m.Image}); err != nil {
					return fail("check box member %s (%s): %w", memberKey, m.Image, err)
				}
			}
		}
	}

	// Step 3: bring up the bed. switch-true (not an if/else chain) over the
	// substrate kind: VM, GROUP (boxless sibling members), or pod/local default.
	switch {
	case isVM:
		// VM beds need libvirt's user-session daemon for the check probes
		// (`charly check libvirt …`, and the `spice:` verb's host-side endpoint
		// resolution) AND for the `backend: libvirt` resolver. Best-effort start before any VM step;
		// the downstream gate surfaces a missing daemon as a clear error.
		startLibvirtUserSession()
		// Best-effort destroy first to clear any lingering libvirt domain
		// from a previous interrupted run, then build → create → deploy-add.
		_ = exec.Command(exe, "vm", "destroy", vmTemplate).Run()
		if err := step("vm-build", []string{"vm", "build", vmTemplate}); err != nil {
			return fail("vm build %s: %w", vmTemplate, err)
		}
		if err := step("vm-create", []string{"vm", "create", vmTemplate}); err != nil {
			return fail("vm create %s: %w", vmTemplate, err)
		}
		deployed = true // VM domain exists — keep it on any later failure
		// `charly vm create` auto-starts the domain, but in-guest sshd takes
		// 30-90s on cold boot; poll until ssh connects so deploy-add starts
		// at a known-ready state. Best-effort: silent on timeout.
		waitForVmSshReady(vmTemplate)
		// Deploy the VM node's own candies AND its nested target:pod children.
		// The VM target's Add applies the candies over SSH (incl. any kernel-driver
		// reboot), then deploys each nested pod as a PERSISTENT in-guest quadlet via
		// plugin-deploy-vm's PostApply (build + cp-box into the guest + the guest's
		// own project-free `charly bundle from-box`). The dispatch routes a VM root
		// node-only (its pod children deploy in-guest, never via a host tree
		// walk), so no --node-only flag is needed and no separate image-transfer
		// step is required.
		if err := step("deploy-add", []string{"bundle", "add", name, vmTemplate}); err != nil {
			return fail("bundle add %s: %w", name, err)
		}
		// plugin-deploy-vm's PostApply (inside the VM deploy-add above) brings up
		// nested target:pod children as in-guest quadlets, but it SKIPS
		// target:local children (they carry no image — they apply candies in
		// place). Deploy each nested local child via the dotted-path dispatch,
		// which applies the child's local-deploy candies into the guest over the
		// NestedExecutor (SSH). Without this, checkLiveTree below would check an
		// un-deployed child and fail. Mirrors the pod-bed nested-child loop.
		for _, childKey := range sortedNestedKeys(node.Children) {
			child := node.Children[childKey]
			if child == nil || (child.Target != "local" && child.Target != "host") {
				continue // pod children handled in-guest by plugin-deploy-vm's PostApply
			}
			if err := step("deploy-"+childKey, []string{"bundle", "add", name + "." + childKey}); err != nil {
				return fail("deploy nested local child %s.%s: %w", name, childKey, err)
			}
		}
	case isGroup:
		// Group bed: no root container — the members (subject + driver) ARE the
		// deployment. Clear any lingering bed + stale members from a prior run;
		// bringUpMembers (in the runtime-check block below) then deploys each
		// member (config+start per pod member, bundle add per local member). There
		// is no root deploy-add/config/start.
		_ = exec.Command(exe, "remove", name, "--purge").Run()
		tearDownMembers(&node)
		deployed = true // members will be brought up — keep state on a later failure
	default:
		// Pod beds → image ref; kind:local beds → local template ref; an EXTERNAL
		// deploy substrate composes its candies via add_candy: and carries no
		// image/template ref (the bundle-add ref falls back to the deploy key).
		addArgs := []string{"bundle", "add", name}
		switch {
		case isExternalDeploy:
			// no ref — add_candy: is the workload
		case isLocal:
			addArgs = append(addArgs, localRef)
		default:
			addArgs = append(addArgs, image)
		}
		addArgs = append(addArgs, "--node-only")
		// Best-effort tear-down of any lingering bed from a previous
		// interrupted/failed run (symmetry with the VM path's pre-destroy
		// above). A failed run now LEAVES the bed up for debugging, so this
		// clears it before the fresh deploy — kept-alive state never blocks
		// a re-run. Silent on the common no-op case. An external deploy tears
		// down via `bundle del` (replays the recorded reverse op); `charly remove`
		// is pod-quadlet-specific and a no-op for it.
		if isExternalDeploy {
			_ = exec.Command(exe, "bundle", "del", name).Run()
		} else {
			// --purge clears any prior bed volumes so each deploy starts fresh
			// (a stale postgres volume would carry a stale password). Safe: a bed's
			// volumes are isolated under its own deploy key, never production's.
			_ = exec.Command(exe, "remove", name, "--purge").Run()
		}
		// Clear any sibling peers left over from a previous interrupted run
		// (symmetry with the bed remove above) so kept-alive peer state never
		// blocks a fresh deploy.
		tearDownMembers(&node)
		// Seed the per-host charly.yml with the bed's project-declared
		// deploy-shaped overrides (port / volume / env / tunnel / security /
		// network) BEFORE charly config runs. The folded bed node is the source of
		// truth, but charly bundle add / charly config otherwise source those fields
		// from the IMAGE LABELS (and gate port writes behind an operator -p), so
		// a bed's declared port: remap would silently fall back to the image
		// default and collide with any same-image deploy already bound to it.
		// (persistBedDeployOverrides itself skips local + external in-place targets.)
		persistBedDeployOverrides(name, node)
		// --node-only: deploy ONLY the bed's root node here. A pod bed's
		// container doesn't exist until `charly start` below, so any nested
		// children (e.g. a `target: android` device that installs apk:
		// packages onto the running emulator) can't deploy yet — they're
		// deployed after start (see the nested-child loop below). Harmless
		// for childless beds (the no-op is identical to a full walk).
		if err := step("deploy-add", addArgs); err != nil {
			return fail("bundle add %s: %w", name, err)
		}
		deployed = true // target registered — keep it on any later failure
		// Pod beds: deploy add registers the entry but does not generate the
		// quadlet or start the service — `charly config` writes the unit,
		// `charly start` activates it. kind:local + external apply candies in place
		// during deploy add, so neither step is needed.
		if !isInPlace {
			if err := step("config", []string{"config", name}); err != nil {
				return fail("config %s: %w", name, err)
			}
			if err := step("start", []string{"start", name}); err != nil {
				return fail("start %s: %w", name, err)
			}
			// `charly start` returns once systemd reports active, but the
			// container's services may not have bound ports yet. Poll until
			// `podman exec true` succeeds (cheap; usually <1s).
			waitForContainerReady(name)
			// Now the substrate is up: deploy any nested children onto it,
			// pre-order. The canonical case is a `target: android` device
			// child whose candies' apk: packages install onto the running
			// emulator (`charly bundle add <bed>.<child>` resolves the child
			// against the started pod's executor). Childless beds skip this.
			for _, childKey := range sortedNestedKeys(node.Children) {
				if err := step("deploy-"+childKey, []string{"bundle", "add", name + "." + childKey}); err != nil {
					return fail("deploy nested child %s.%s: %w", name, childKey, err)
				}
			}
		}
	}

	// checkLiveTree runs `charly check live` against the bed's substrate AND every
	// nested child through the multi-hop NestedExecutor chain, so a nested
	// child's BAKED candy/box check (e.g. the selkies candy's frame-not-black
	// + encoder-active checks on a nested selkies-kde pod) is actually exercised
	// against its real venue. Without this, `charly check run` deploys nested
	// children (above) but never evaluates them — their coverage is silently
	// skipped, which is exactly why nested beds used to hand-roll guest-side
	// `podman exec <child>` probes. For a flat bed (no children) it is exactly
	// the prior parent-only check. stepLabel disambiguates initial vs rebuild.
	checkLiveTree := func(stepLabel string) error {
		for i, ref := range bedCheckLiveRefs(name, node.Children) {
			label := stepLabel
			if i > 0 {
				label = stepLabel + "-" + ref[len(name)+1:] // childKey after "<name>."
			}
			if err := stepReady(label, []string{"check", "live", ref}, recoverVMIfDown); err != nil {
				return err
			}
		}
		return nil
	}

	// Step 4: full-stack live check against the deployed target's venue —
	// container/VM via podman-exec/SSH, or the HOST filesystem (ShellExecutor)
	// for kind:local. A local bed's deploy-scope `check:` probes now run through
	// `charly check live <name>`'s local dispatch (runLocalCheck); the host is ready
	// the moment deploy-add returns, so stepReady passes on the first poll.
	//
	// Readiness retry: a fresh service may still be starting (e.g. Immich's
	// first-run DB migration runs minutes before the API binds). stepReady
	// polls check-live until it passes or the deadline, so we wait for real
	// readiness instead of racing a fixed sleep.
	// Bring up sibling members (companion DRIVER deployments — e.g. a Chrome pod)
	// ALONGSIDE the substrate, ONCE, regardless of substrate kind (pod / vm /
	// local) — the subject's `on: <member>` checks drive through them. Members are
	// instruments, NEVER check-live'd (excluded from bedCheckLiveRefs). The SAME
	// bringUpMembers helper serves the operator deploy path (R3). One call, not
	// one per kind.
	// Deploy/runtime acceptance — gated out at check_level: none|build (the bed
	// then proves only that the image builds + deploys). Members are instruments
	// for the runtime probes, so bring-up is gated with them.
	if runRuntimeCheck {
		if err := bringUpMembers(&node); err != nil {
			return fail("bring up peers for %s: %w", name, err)
		}
		if err := checkLiveTree("check-live"); err != nil {
			return fail("check live %s: %w", name, err)
		}

		// Step 4b: Agent Driven Evaluation acceptance — run the bed image's baked
		// `plan:` steps as acceptance tests. This is
		// the opt-in plan gate: a no-op PASS when the image bakes no plan steps,
		// real coverage when it does. At check_level: noagent it runs --no-agent
		// (deterministic, free); at check_level: agent the prose-step agent grader
		// runs too (featureRunArgs drops --no-agent). Pod beds only: VM/local
		// deployments carry no image-baked description label to run.
		if !isVM && !isLocal && image != "" {
			if err := step("feature-run", featureRunArgs()); err != nil {
				return fail("feature run %s: %w", name, err)
			}
		}
	}

	// Step 5: fresh-update re-verify (the R10 acceptance gate). Suppressed
	// by --no-rebuild for fast smoke that exercises the dispatcher only.
	if !opts.NoRebuild && isGroup {
		// Group bed: no root container to `charly update`. The R10 fresh-rebuild
		// gate re-builds each member image, re-brings-up the members, and
		// re-check-lives — mirroring the initial group deploy.
		for _, memberKey := range sortedMemberKeys(node.Members) {
			m := node.Members[memberKey]
			if m == nil || m.Image == "" {
				continue
			}
			if err := step("update-image-"+memberKey, []string{"box", "build", m.Image, "--dev-local-pkg"}); err != nil {
				return fail("rebuild member image %s (%s): %w", memberKey, m.Image, err)
			}
		}
		tearDownMembers(&node)
		if runRuntimeCheck {
			if err := bringUpMembers(&node); err != nil {
				return fail("re-bring up members for %s: %w", name, err)
			}
			if err := checkLiveTree("check-live-rebuild"); err != nil {
				return fail("check live (fresh rebuild) %s: %w", name, err)
			}
		}
	} else if !opts.NoRebuild {
		if err := step("update", []string{"update", name}); err != nil {
			return fail("update %s: %w", name, err)
		}
		// For a nested bed, the fresh rebuild discards the substrate's
		// previously-deployed children (a rebuilt pod / VM guest is empty), so
		// the nested material MUST be re-applied and check-live re-run to
		// actually re-verify the new functionality on the rebuild — otherwise
		// the post-update state is unexercised. (A flat bed's `charly update`
		// succeeding is itself the rebuild proof; its baked deploy-scope check
		// needs no re-deploy.)
		if runRuntimeCheck && !isInPlace && len(node.Children) > 0 {
			if isVM {
				// `charly update` recreated the libvirt domain; the qcow2 disk (and
				// thus the applied guest candies, the nested pod's quadlet, and
				// its loaded image) persists across the recreate. The nested pod
				// is a PERSISTENT in-guest quadlet with lingering enabled, so it
				// auto-starts on the fresh boot — no re-assert needed. Just wait
				// for ssh; the rebuild check-live then PROVES the nested pod
				// survived the domain recreate (the Cutover 2 persistence gate).
				waitForVmSshReady(vmTemplate)
			} else {
				waitForContainerReady(name)
				for _, childKey := range sortedNestedKeys(node.Children) {
					if err := step("redeploy-"+childKey, []string{"bundle", "add", name + "." + childKey}); err != nil {
						return fail("re-deploy nested child %s.%s (fresh rebuild): %w", name, childKey, err)
					}
				}
			}
			if err := checkLiveTree("check-live-rebuild"); err != nil {
				return fail("check live (fresh rebuild) %s: %w", name, err)
			}
		}
		// Re-run the bed image's baked plan steps on the fresh rebuild (pod
		// beds) — the ADE acceptance gate against the new image, at the box's
		// check_level depth. No-op pass when the image bakes no plan steps.
		if runRuntimeCheck && !isVM && !isLocal && image != "" {
			waitForContainerReady(name)
			if err := step("feature-run-rebuild", featureRunArgs()); err != nil {
				return fail("feature run (fresh rebuild) %s: %w", name, err)
			}
		}
	}

	// Step 6: tear down (suppressed by --keep). Errors are recorded but
	// don't fail the overall run — the live-check pass is already on record.
	cleanup()

	writeBedSummary(logDir, res)
	if !res.OK {
		return res, fmt.Errorf("bed %s: one or more steps failed", name)
	}
	return res, nil
}

// runCapture runs the given charly subcommand, capturing combined stdout+stderr
// and returning the bytes plus the exec error.
func runCapture(exe string, args []string) ([]byte, error) {
	return runCaptureCtx(context.Background(), exe, args)
}

// runCaptureCtx is the context-aware capture used by stepReady's pollUntil cond
// so a hung `charly check live` pass is bounded by the per-attempt context (R4
// never-hang). runCapture delegates to it (R3, one implementation).
func runCaptureCtx(ctx context.Context, exe string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, exe, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	return buf.Bytes(), err
}

// waitForVmSshReady gates on the VM being SSH-reachable AND cloud-init having
// settled, using the SAME deterministic SSHExecutor preflight the VM check-live
// path (check_cmd.go) and the external vm deploy walk run — NOT a fixed sleep. WaitForSSH
// polls until sshd answers; WaitForCloudInit retries until an ssh connection
// survives a `cloud-init status` poll (the deterministic cloud-init-settled
// signal — so deploy-add never races a still-running first-boot pacman). vmName
// is the kind:vm entity name. Best-effort: silent on timeout — the downstream
// deploy-add surfaces the real error.
func waitForVmSshReady(vmName string) {
	gate := &SSHExecutor{Host: VmSshAlias(vmName), ConnectTimeout: 5}
	ctx := context.Background()
	if err := gate.WaitForSSH(ctx); err != nil {
		return
	}
	_ = gate.WaitForCloudInit(ctx)
}

// waitForContainerReady gates on the container being exec-able AND its
// supervisord-managed children having left their transitional states, so a
// one-shot check-live port/service probe never races a child that has not yet
// bound. `charly start` returns when systemd reports the service active, but
// supervisord's autostart children are still STARTING for a moment after. This
// polls `supervisorctl status` until no child is STARTING/BACKOFF (a child binds
// its port the instant it reaches RUNNING) instead of sleeping a fixed,
// host-tuned interval. Images without supervisord settle immediately. Best-effort:
// silent on timeout (the next check-live step surfaces the real failure).
func waitForContainerReady(bed string) {
	containerName := "charly-" + bed
	// supervisorStatus reports __NOSUP__ when the image has no supervisorctl, so
	// "no supervisord" is distinguishable from "socket not up yet".
	const supervisorStatus = `command -v supervisorctl >/dev/null 2>&1 || { echo __NOSUP__; exit 0; }; supervisorctl status 2>&1`
	// MONOTONIC readiness via the unified pollUntil primitive (poll.go): the
	// progress marker is the count of SETTLED children — it climbs as children
	// reach RUNNING, so a slow startup under heavy parallel load is waited for
	// (the no-progress watchdog resets on each new settled child); a child
	// crash-looping back to BACKOFF drops the count below its high-water, so the
	// watchdog correctly does NOT treat the flap as progress and the bed stalls
	// out instead of hiding the fault. Replaces the fixed 30s deadline (the most
	// load-fragile in the old set). Best-effort: silent on stall/cap (the next
	// check-live step surfaces the real failure).
	cfg := loadedReadiness().Wait("container-ready "+bed, PollLocal)
	_ = pollUntil(context.Background(), cfg, func(actx context.Context) (bool, float64, error) {
		if exec.CommandContext(actx, "podman", "exec", containerName, "true").Run() != nil {
			return false, 0, nil // container not exec-able yet
		}
		out, _ := exec.CommandContext(actx, "podman", "exec", containerName, "sh", "-c", supervisorStatus).CombinedOutput()
		if bytes.Contains(out, []byte("__NOSUP__")) {
			return true, 0, nil // no supervisord — nothing to settle
		}
		settled := float64(bytes.Count(out, []byte("RUNNING")) + bytes.Count(out, []byte("STOPPED")) +
			bytes.Count(out, []byte("EXITED")) + bytes.Count(out, []byte("FATAL")))
		if bytes.Contains(out, []byte("STARTING")) || bytes.Contains(out, []byte("BACKOFF")) {
			return false, settled, nil // children still coming up
		}
		if settled > 0 {
			return true, settled, nil // supervisord answered + nothing transitional
		}
		return false, 0, nil // supervisord control socket not up yet
	})
}

// writeBedSummary emits a YAML summary alongside the per-step logs.
// Hand-rolled to keep the file dependency-free and diff-friendly.
func writeBedSummary(dir string, res *bedRunResult) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "bed: %s\n", res.Bed)
	fmt.Fprintf(&buf, "calver: %s\n", res.CalVer)
	fmt.Fprintln(&buf, "steps:")
	var total time.Duration
	for _, s := range res.Step {
		fmt.Fprintf(&buf, "  - name: %s\n", s.Name)
		fmt.Fprintf(&buf, "    duration_seconds: %d\n", int(s.Duration.Round(time.Second)/time.Second))
		fmt.Fprintf(&buf, "    ok: %t\n", s.OK)
		total += s.Duration
	}
	fmt.Fprintf(&buf, "total_seconds: %d\n", int(total.Round(time.Second)/time.Second))
	fmt.Fprintf(&buf, "ok: %t\n", res.OK)

	path := filepath.Join(dir, "summary.yml")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "charly check run %s: writing %s: %v\n", res.Bed, path, err)
	}
}
