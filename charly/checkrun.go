package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

// CheckStatus is the outcome of a single Check.
type CheckStatus int

const (
	TestPass CheckStatus = iota
	TestFail
	TestSkip
)

func (s CheckStatus) String() string {
	switch s {
	case TestPass:
		return "pass"
	case TestFail:
		return "fail"
	case TestSkip:
		return "skip"
	}
	return "unknown"
}

// CheckResult captures the outcome of running a single Check.
//
// Attempts and TotalElapsed are populated only when the check had an
// `eventually:` modifier (retry loop). Attempts=1 + TotalElapsed==Elapsed
// for checks that ran exactly once. Reporters surface these when Attempts>1
// so slow startup paths are visible ("PASS in 5 attempts over 12.3s").
type CheckResult struct {
	Op           *Op
	Verb         string
	Status       CheckStatus
	Message      string
	Elapsed      time.Duration
	Attempts     int           `json:"attempts,omitempty"`
	TotalElapsed time.Duration `json:"total_elapsed,omitempty"`

	// DeadlineExceeded marks a result whose probe was killed by hitting its OWN
	// per-attempt deadline (probeNeverHang), NOT an external infra interruption. The
	// group-kill in runCaptureCmd surfaces the deadline SIGKILL as a signal-kill, so
	// without this flag the killed-probe retry (probeWasKilled) would futilely re-run a
	// probe that will only re-hang and re-hit the same deadline. An authoritative
	// "too slow" failure — never retried.
	DeadlineExceeded bool `json:"-"`

	// CapturedValue is the value stashed under `capture:` for consumption
	// by downstream steps in the same plan run. Empty when Capture was
	// unset or the check did not pass (captures are recorded only on
	// final PASS — failing `eventually:` attempts don't pollute).
	CapturedValue string `json:"captured_value,omitempty"`
}

// RunMode selects routing rules for a Run() invocation.
//
//   - RunModeLive: charly check live — against a running container. In-container
//     probes via Exec; host-side verbs (http/dns/addr) from the charly process.
//   - RunModeBox: charly check box — against a disposable container
//     (podman run --rm). All probes via Exec; host-side reachability is
//     not meaningful and those checks are skipped.
type RunMode int

const (
	RunModeLive RunMode = iota
	RunModeBox
)

// Executor + ContainerExecutor + ImageExecutor + VmTestExecutor were
// deleted in the 2026-04 executor-hierarchy cutover. The runner now
// uses DeployExecutor (deploy_executor.go) directly — chains for
// nested topologies (host → ssh-vm → podman-exec-pod → podman-exec-
// nested-pod) come from ResolveDeployChain (deploy_chain.go). Every
// former call site of `r.Exec.RunCapture(ctx, cmd)` became
// `r.Exec.RunCapture(ctx, cmd)` with identical (stdout, stderr, exit,
// err) return semantics.
//
// The `runCapture(cmd *exec.Cmd)` helper that used to live here moved
// to deploy_executor.go as `runCaptureCmd` so every DeployExecutor
// implementation can share it. asExitError moved alongside as
// asExitErrorDeploy. Both are package-private and used by
// ShellExecutor.RunCapture / SSHExecutor.RunCapture.

// Runner wires together the execution context for one pass of checks.
//
// Image and Instance are the user-supplied names under RunModeLive. They are
// snapshotted into the CheckEnv handed to each out-of-process check verb (provider_checkenv.go)
// so an EXEC-based external verb (record/dbus/wl) can reach the live venue over the reverse
// channel; they are empty under RunModeBox, which causes those verbs to skip with a clear
// message — they need a running container with port mappings. (No in-proc verb builds
// `charly check <verb>` CLI invocations anymore — every live-container verb is external.)
type Runner struct {
	Exec        DeployExecutor
	Resolver    *CheckVarResolver
	Mode        RunMode
	HTTPClient  *http.Client
	DialTimeout time.Duration
	// ProbeTimeout is the per-probe never-hang ceiling: each probe attempt in
	// runOne runs under context.WithTimeout(ctx, ProbeTimeout) so a wedged probe
	// (a hung `podman exec` / black-holed ssh) is cancelled INDIVIDUALLY and the
	// pass continues to the next probe — instead of hanging the whole pass until
	// the bed runner's outer per-attempt SIGKILLs the entire `charly check live`
	// subprocess (the old hard-timeout-not-pooling failure under heavy load).
	// Zero falls back to readinessPerAttemptFallback (probeNeverHang); a longer
	// author-declared `timeout:` on a probe is honored over this floor.
	ProbeTimeout time.Duration
	Box          string
	Instance     string
	// VmName is the resolved vm: ENTITY name for a VM deployment — the deploy/bed
	// name remapped to its entity (uf.Bundle[box].From). Box stays the deploy name
	// (container + DEPLOY_NAME identity); the operator-side libvirt/spice verbs must
	// address the live libvirt domain charly-<VmName>, so they read vmTargetName().
	// Empty for non-VM deployments, where vmTargetName() falls back to Box.
	VmName string
	// Distros is the image's distro tag list (e.g. ["fedora:43", "fedora"]
	// or ["arch"]). Used by the `package:` verb's PackageMap resolution
	// to pick a distro-specific package name when names diverge across
	// distros (e.g. openssh-server on Fedora vs openssh on Arch).
	Distros []string

	// CandyDirs maps a candy name → its resolved source directory. Used to
	// anchor a relative committed-APK path in an `adb: install` / `appium:
	// install-app` check (apk: ./tests/data/...) against the authoring candy's
	// source tree, so a check resolves the same way whether the candy is local
	// or fetched via @github (the SAME walk-up the deploy path uses, R3).
	CandyDirs map[string]string

	// CandyScanErr is the error (if any) from building CandyDirs. It is NOT
	// fatal on its own — only an apk-anchoring check consults it, and only then
	// does resolveCheckApk fail HARD with this as the root cause. An apk-free
	// check run is unaffected.
	CandyScanErr error

	// VerifyOnly, when true, restricts a RunPlan walk to the idempotent
	// verification steps (check:/agent-check:) and SKIPS mutating steps
	// (run:/agent-run:). This is the `charly check live` / `charly check box`
	// mode — verify a running/disposable target without re-provisioning.
	// False (the default) runs every step in order (provision-and-verify).
	VerifyOnly bool

	// SkipDeterministicRun, when true, SKIPS deterministic run: (install-
	// timeline) steps while still running check:/agent-check: and the
	// agent-graded agent-run:. This is the `charly box/check feature run`
	// (ADE acceptance "Run") mode: the install already happened at image-build,
	// so re-executing run: against a built/deployed target is redundant AND
	// fails for build-context steps (e.g. `pip install /ctx/...`, where /ctx
	// exists only during the Containerfile build). Distinct from VerifyOnly
	// (which also skips agent-run:); the iterate (kind:score) loop sets neither,
	// so its runtime-context run: steps still run. See /charly-check:check ADE.
	SkipDeterministicRun bool

	// Scenario carries the per-run capture/var context when the runner is
	// driving a plan: (from description_run.go). Nil under classical bare-Op
	// runs — captures/${STEP_ID}/etc. stay absent and behaviour is unchanged.
	Scenario *ScenarioContext

	// Grader, when set, judges an agent step (agent-run:/agent-check:) instead
	// of the default skip/--strict-fail. The agent grader
	// (check_feature_grader.go) spawns the configured kind:agent CLI to probe
	// the live target and return a pass/fail verdict with evidence. Set only by
	// `charly check feature run <deployment>` against a running deployment the
	// agent can reach; nil elsewhere (agent steps then advisory-skip).
	Grader StepGrader

	// TargetResolver, when set, is called to obtain a (resolver, exec)
	// pair for a given `on:` target name. Enables multi-target plan runs
	// (the `on:` step modifier). Classical `tests:` runs leave this nil
	// and use the Runner's static Resolver+Exec pair throughout.
	//
	// The caller (usually description_run.go) decides the lookup policy
	// — typically a map of deployment/image names to pre-initialized
	// executors. Returning (nil, nil, nil) means "unknown target"; the
	// runner then reports the step as FAIL with a clear message.
	TargetResolver func(target string) (*CheckVarResolver, DeployExecutor, error)

	// HostVars carries pre-resolved cross-deployment address variables —
	// ${HOST:name} and ${HOST:name:port} (check_members.go) — that
	// let a driven probe (a check with `on: <driver>`) TARGET a SEPARATE
	// SUBJECT deployment over the shared `charly` network or the host. Overlaid by
	// effectiveEnv onto WHATEVER resolver is active (primary, on:-swapped, or a
	// harness bucket), so cross-deployment addressing is identical across
	// `charly check live`, kind:check beds, and AI-iteration runs. Nil under classical runs
	// with no ${HOST:<member>} refs (no overlay, behaviour unchanged).
	HostVars map[string]string
	// hostCleanups tears down anything opened while resolving HostVars (an
	// ssh -L forward for a ${HOST} VM/host subject). Run via
	// CloseHosts() — the check command defers it at run end.
	hostCleanups []func()
}

// CloseHosts tears down any resources opened while resolving ${HOST:<member>} address
// variables (ssh -L forwards). Safe to call when none were opened.
func (r *Runner) CloseHosts() {
	for _, c := range r.hostCleanups {
		if c != nil {
			c()
		}
	}
	r.hostCleanups = nil
}

// vmTargetName returns the name the host-side check verbs hand to the
// out-of-process vm/spice plugins as the libvirt-domain target: the resolved VM
// ENTITY name (VmName) when set, else the deploy name (Box). The plugin prefixes
// charly- onto whatever it receives and cannot LoadUnified to remap a deploy name
// to its vm: entity itself, so the host threads the already-resolved entity name
// through. A pod deployment leaves VmName empty, so its verbs correctly address
// charly-<deploy-name>.
func (r *Runner) vmTargetName() string {
	if r.VmName != "" {
		return r.VmName
	}
	return r.Box
}

// RunLive runs `checks` as a LIVE cross-deployment check. It is the SINGLE entry
// point every host-context live-check path (a pod / VM / local SUBJECT) shares,
// so cross-deployment support is wired generically in ONE place, never per kind
// (R3). It wires the `on:` driver TargetResolver (liveTargetResolver resolves a
// driver of ANY kind via resolveCheckVenue), pre-resolves the ${HOST:<member>} subject
// addresses (applyHostVars), runs, and tears down any host endpoints opened.
// The harness scorer (check_runner_live.go) keeps its OWN resolver — it runs
// against sandbox-NESTED pods, a genuinely different venue context, not a
// duplicate of this host-context path.
func (r *Runner) RunLive(ctx context.Context, checks []Op, instance string) []CheckResult {
	r.TargetResolver = liveTargetResolver(instance)
	applyHostVars(r, checks, instance)
	defer r.CloseHosts()
	return r.Run(ctx, checks)
}

// NewRunner constructs a Runner with sensible defaults. Caller passes a
// DeployExecutor appropriate for the mode — typically the chain returned
// by ResolveDeployChain (deploy_chain.go). For RunModeLive probes against
// a single running container, that's NestedExecutor{Parent: Local, Jump:
// PodmanExec{charly-name}}; for RunModeBox, ImageChain(engine, ref).
func NewRunner(exec DeployExecutor, resolver *CheckVarResolver, mode RunMode) *Runner {
	return &Runner{
		Exec:         exec,
		Resolver:     resolver,
		Mode:         mode,
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
		DialTimeout:  3 * time.Second,
		ProbeTimeout: loadedReadiness().PerAttemptFor(PollLocal),
	}
}

// probeNeverHang is the per-probe-attempt never-hang ceiling. It is NOT the
// probe's semantic timeout — the http client (10s), dial timeout (3s), a verb's
// own `timeout:`, and the `eventually:` retry loop all operate INSIDE it. It is
// the kill-switch for a probe that wedges in its data phase (a hung
// `podman exec`, a black-holed ssh) so one stuck probe cannot hang the whole
// multi-probe pass. A longer author-declared `timeout:` is honored over the
// floor so a legitimately slow probe is never cut short.
func (r *Runner) probeNeverHang(c *Op) time.Duration {
	floor := r.ProbeTimeout
	if floor <= 0 {
		floor = readinessPerAttemptFallback
	}
	if c != nil && c.Timeout != "" {
		if d, err := time.ParseDuration(c.Timeout); err == nil && d+30*time.Second > floor {
			return d + 30*time.Second
		}
	}
	return floor
}

// Run executes the supplied checks sequentially and returns per-check
// results. Does not short-circuit on failure — the report should show
// every check's outcome for CI ergonomics.
func (r *Runner) Run(ctx context.Context, checks []Op) []CheckResult {
	results := make([]CheckResult, 0, len(checks))
	for i := range checks {
		results = append(results, r.runOne(ctx, &checks[i]))
	}
	return results
}

// runOne handles all the per-check housekeeping (verb resolution, skip
// handling, variable expansion, routing) and dispatches to a verb handler.
//
// Two BDD-era behaviours layer on top of the classical path:
//
//  1. `on:` target dispatch — if the check specifies a non-default
//     target AND r.TargetResolver is set, runOne temporarily swaps
//     r.Exec / r.Resolver for the duration of the dispatch. Classical
//     tests: runs pass nil TargetResolver and never hit this path.
//  2. `eventually:` retry wrapper — when set, the verb dispatch is
//     called repeatedly until pass or deadline.
//
//nolint:gocyclo // verb dispatch router with on: target-swap and eventually: retry wrapper; branching is essential to the execution model
func (r *Runner) runOne(ctx context.Context, c *Op) CheckResult {
	start := time.Now()
	kind, err := c.Kind()
	result := CheckResult{Op: c, Verb: kind}
	if err != nil {
		result.Status = TestFail
		result.Message = err.Error()
		result.Elapsed = time.Since(start)
		result.Attempts = 1
		result.TotalElapsed = result.Elapsed
		return result
	}
	if c.Skip {
		result.Status = TestSkip
		result.Message = "skip: true"
		result.Elapsed = time.Since(start)
		result.Attempts = 1
		result.TotalElapsed = result.Elapsed
		return result
	}
	// exclude_distros: skip when any of the image's distro tags intersects
	// with the exclusion list. Used for probes that are only meaningful on
	// some distros (e.g. a binary that a given distro renames or drops).
	if len(c.ExcludeDistros) > 0 && len(r.Distros) > 0 {
		for _, imgTag := range r.Distros {
			if slices.Contains(c.ExcludeDistros, imgTag) {
				result.Status = TestSkip
				result.Message = fmt.Sprintf("excluded on distro %q", imgTag)
				result.Elapsed = time.Since(start)
				result.Attempts = 1
				result.TotalElapsed = result.Elapsed
				return result
			}
		}
	}

	// Context-vs-mode skip — the unified-Op replacement for the old
	// scope:build↔check-box / scope:deploy↔check-live split. `charly check box`
	// (RunModeBox) runs against a disposable BUILD container, so it runs only
	// build-context steps; `charly check live` (RunModeLive) runs against a
	// RUNNING target, so it runs runtime-context steps. A step whose effective
	// context excludes the run's context is SKIPPED with a reason (e.g. a
	// `context: [runtime]` port/service probe in check box — no service runs in
	// a disposable build container).
	wantCtx := CtxRuntime
	modeName := "live"
	if r.Mode == RunModeBox {
		wantCtx, modeName = CtxBuild, "box"
	}
	if !opInContext(c, wantCtx) {
		result.Status = TestSkip
		result.Message = fmt.Sprintf("context %v not active in %s mode", opEffectiveContexts(c), modeName)
		result.Elapsed = time.Since(start)
		result.Attempts = 1
		result.TotalElapsed = result.Elapsed
		return result
	}
	// Per-step VENUE dispatch (loader-derived from tree position — the former
	// authored `on:`). Swap executor + resolver + image for the duration of this
	// check only; restore on return. The self-swap guard (`c.Venue != r.Box`)
	// skips the swap when the step's venue is already the active target: the
	// scored-step path (check_runner_live.go) pre-buckets by venue and sets r.Box
	// to the bucket venue, so its in-bucket steps need no re-swap; the
	// deterministic path (charly check live <bed>) swaps only for a step whose
	// venue differs from the bed's default target. When r.TargetResolver is nil
	// (classical no-tree path), Resolver+Exec stay as-is.
	//
	// The swap also retargets r.Box so the out-of-process EXEC-based verbs (record/dbus/wl,
	// whose CheckEnv snapshot carries the venue) AND the port-based pre-resolvers
	// (cdp/vnc/mcp/spice/kube, which read r.Box for the venue's endpoint) route against the
	// venue's pod, not the plan run's default pod.
	origExec, origResolver, origImage := r.Exec, r.Resolver, r.Box
	if c.Venue != "" && c.Venue != r.Box && r.TargetResolver != nil {
		newResolver, newExec, terr := r.TargetResolver(c.Venue)
		if terr != nil {
			result.Status = TestFail
			result.Message = fmt.Sprintf("venue %q — %v", c.Venue, terr)
			result.Elapsed = time.Since(start)
			result.Attempts = 1
			result.TotalElapsed = result.Elapsed
			return result
		}
		if newExec != nil {
			r.Exec = newExec
		}
		if newResolver != nil {
			r.Resolver = newResolver
		}
		r.Box = c.Venue
		defer func() {
			r.Exec = origExec
			r.Resolver = origResolver
			r.Box = origImage
		}()
	}

	// Expand variables in-place on a copy so repeated runs over the same
	// check list don't accumulate substitutions. The env is derived by
	// overlaying the ScenarioContext (captures + ids) onto the resolver
	// base — so classical tests: with Scenario==nil see exactly today's
	// behavior.
	expanded := *c
	env := r.effectiveEnv()
	missing := opExpandVars(&expanded, env)
	if len(missing) > 0 {
		// An unresolved cross-deployment var (${HOST}/${HOST})
		// means the peer/subject this probe targets is UNREACHABLE — the
		// probe's whole premise failed, so the check FAILS. A SKIP there would
		// be a fake pass (the bed must NOT go green on an unreachable peer).
		// Other unresolved vars stay a legitimate SKIP: a deploy-only var under
		// build scope, an unmounted volume — inputs that genuinely don't apply
		// to this run, not a failed dependency.
		if hostMissing := filterHostVars(missing); len(hostMissing) > 0 {
			result.Status = TestFail
			result.Message = fmt.Sprintf("peer unreachable — unresolved cross-deployment variable(s): %s", strings.Join(hostMissing, ", "))
		} else {
			result.Status = TestSkip
			result.Message = fmt.Sprintf("unresolved variables: %s", strings.Join(missing, ", "))
		}
		result.Elapsed = time.Since(start)
		result.Attempts = 1
		result.TotalElapsed = result.Elapsed
		return result
	}

	// Verb dispatch, wrapped in the `eventually:` retry when requested.
	dispatch := func() CheckResult {
		// Per-probe never-hang: bound THIS attempt so a wedged probe (a hung
		// podman exec / black-holed ssh) is cancelled individually and the pass
		// continues — instead of relying on the bed runner's whole-pass timeout
		// to SIGKILL the entire 100+-probe `charly check live` subprocess (the
		// old hard-timeout-not-pooling failure under heavy load). runWithEventually
		// calls dispatch once per attempt, so each retry gets a FRESH bound; the
		// author's timeout:/eventually: operate inside it. Shadows ctx for the
		// rest of this closure so every r.runX(ctx, …) below is bounded.
		ctx, cancel := context.WithTimeout(ctx, r.probeNeverHang(&expanded))
		defer cancel()
		var dr CheckResult
		// do-mode branch: act on a state-provision verb → execute the
		// create/configure. Action verbs (command/http/dbus/cdp/…) act in their
		// own handler, so do:act there falls through to the assert dispatch
		// below (the handler IS the act). Agent steps never reach runOne —
		// they route to the grader in runUnit (description_run.go).
		if opEffectiveDo(&expanded) == DoAct {
			if act, ok := r.runProvisionAct(ctx, &expanded, kind); ok {
				return act
			}
		}
		// Verb dispatch is the provider registry (the switch is gone — C1). Every
		// built-in verb is a CheckVerbProvider (verb_builtins.go); an out-of-tree
		// plugin verb arrives via the generic `plugin:` envelope (the pluginVerb
		// provider → runPluginVerb).
		if prov, ok := providerRegistry.ResolveVerb(kind); !ok {
			dr.Status = TestSkip
			dr.Message = fmt.Sprintf("unknown verb %q", kind)
		} else if cv, ok := prov.(CheckVerbProvider); ok {
			dr = cv.RunVerb(ctx, r, &expanded)
		} else {
			// An OUT-OF-PROCESS verb provider (a grpcProvider, not a CheckVerbProvider):
			// dispatch the live verb word to the Invoke envelope with the full Op — the
			// external-charly-verb path. The verb's params stay authored in #Op (no
			// migration); the plugin reads them from params_json.
			dr = r.invokeVerbProvider(ctx, prov, kind, &expanded)
		}
		// If THIS attempt's per-attempt deadline (probeNeverHang) fired, the probe ran
		// too long — an AUTHORITATIVE "too slow" failure, NOT an infra interruption.
		// runCaptureCmd's group-kill surfaces the deadline SIGKILL as a signal-kill;
		// flag it so the killed-probe retry (description_eventually.go) does NOT futilely
		// re-run a probe that will only re-hang and re-hit the same deadline.
		if ctx.Err() == context.DeadlineExceeded {
			dr.DeadlineExceeded = true
		}
		return dr
	}

	result = runWithEventually(ctx, &expanded, dispatch)
	result.Op = c
	result.Verb = kind
	result.Elapsed = time.Since(start)
	// runWithEventually sets TotalElapsed relative to its own start time;
	// prefer that for multi-attempt cases. For single-attempt, Elapsed ≈
	// TotalElapsed so the caller-facing fields stay consistent.
	if result.TotalElapsed == 0 {
		result.TotalElapsed = result.Elapsed
	}

	return result
}

// effectiveEnv builds the variable-expansion env map for the current
// check. When a ScenarioContext is attached, captures + STEP_ID are
// overlaid on top of the resolver's base env — keeping
// classical tests: behaviour unchanged (nil Scenario → no overlay).
func (r *Runner) effectiveEnv() map[string]string {
	var base map[string]string
	if r.Resolver != nil {
		base = r.Resolver.Env
	}
	if r.Scenario == nil && len(r.HostVars) == 0 {
		return base
	}
	// Copy-on-overlay so the resolver's shared Env map stays clean across
	// plan runs. Cross-deployment ${HOST:<member>} addresses overlay first (they are
	// per-run, target-independent), then plan-run captures (which win on the
	// rare key collision).
	env := make(map[string]string, len(base)+len(r.HostVars)+2)
	maps.Copy(env, base)
	maps.Copy(env, r.HostVars)
	if r.Scenario != nil {
		r.Scenario.ApplyToEnv(env)
	}
	return env
}

// ---------------------------------------------------------------------------
// command verb
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Result helpers
// ---------------------------------------------------------------------------

func passf(c *Op, msg string) CheckResult {
	return CheckResult{Op: c, Status: TestPass, Message: msg}
}

func failf(c *Op, format string, args ...any) CheckResult {
	return CheckResult{Op: c, Status: TestFail, Message: fmt.Sprintf(format, args...)}
}

func skipf(c *Op, msg string) CheckResult {
	return CheckResult{Op: c, Status: TestSkip, Message: msg}
}

// ---------------------------------------------------------------------------
// Report rendering — text / JSON / TAP v13.
// ---------------------------------------------------------------------------

// FormatResultsText writes a human-readable summary of results to w and
// returns the number of failures.
func FormatResultsText(w io.Writer, results []CheckResult) int {
	passes, fails, skips := 0, 0, 0
	for _, r := range results {
		glyph := "?"
		switch r.Status {
		case TestPass:
			glyph = "✓"
			passes++
		case TestFail:
			glyph = "✗"
			fails++
		case TestSkip:
			glyph = "⚠"
			skips++
		}
		verb := r.Verb
		subject := firstNonEmpty(r.Op.PluginInputStr("file"), r.Op.PluginInputStr("http"), r.Op.Command, r.Op.PluginInputStr("command"), r.Op.PluginInputStr("addr"))
		fmt.Fprintf(w, "%s %s %s — %s\n", glyph, verb, subject, r.Message)
		if r.Op.Origin != "" && r.Status == TestFail {
			fmt.Fprintf(w, "  from %s\n", r.Op.Origin)
		}
	}
	fmt.Fprintf(w, "%d passed · %d failed · %d skipped\n", passes, fails, skips)
	return fails
}

// FormatResultsJSON emits a structured report suitable for CI consumption.
// Returns the number of failures.
func FormatResultsJSON(w io.Writer, results []CheckResult) int {
	type entry struct {
		Verb    string `json:"verb"`
		Status  string `json:"status"`
		Origin  string `json:"origin,omitempty"`
		Subject string `json:"subject,omitempty"`
		Message string `json:"message,omitempty"`
	}
	out := make([]entry, 0, len(results))
	fails := 0
	for _, r := range results {
		subject := firstNonEmpty(r.Op.PluginInputStr("file"), r.Op.PluginInputStr("http"), r.Op.Command, r.Op.PluginInputStr("command"), r.Op.PluginInputStr("addr"))
		if r.Status == TestFail {
			fails++
		}
		out = append(out, entry{
			Verb:    r.Verb,
			Status:  r.Status.String(),
			Origin:  r.Op.Origin,
			Subject: subject,
			Message: r.Message,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	return fails
}

// FormatResultsTAP emits TAP v13. Returns the number of failures.
func FormatResultsTAP(w io.Writer, results []CheckResult) int {
	fails := 0
	fmt.Fprintf(w, "TAP version 13\n1..%d\n", len(results))
	for i, r := range results {
		subject := firstNonEmpty(r.Op.PluginInputStr("file"), r.Op.PluginInputStr("http"), r.Op.Command, r.Op.PluginInputStr("command"), r.Op.PluginInputStr("addr"))
		label := fmt.Sprintf("%s %s - %s", r.Verb, subject, r.Message)
		switch r.Status {
		case TestPass:
			fmt.Fprintf(w, "ok %d - %s\n", i+1, label)
		case TestSkip:
			fmt.Fprintf(w, "ok %d - %s # SKIP %s\n", i+1, label, r.Message)
		case TestFail:
			fails++
			fmt.Fprintf(w, "not ok %d - %s\n", i+1, label)
		}
	}
	return fails
}
