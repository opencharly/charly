package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"time"

	"github.com/opencharly/sdk/kit"
)

// CheckStatus, CheckResult, and the pass/fail/skip verdict constants live ONCE in sdk/kit —
// they are the check engine's result model, shared with every plugin candy that runs a plan.
// These are the package-main bindings; core's call sites (12 files) are unchanged.
//
// kit.Status is the single pass/fail/skip enum: the former core CheckStatus.String() and the
// kit.Status ⇆ CheckStatus converter are gone — the two enums were the same three-valued
// type, so a verb's kit.Result verdict now flows into a CheckResult with no conversion.
type CheckStatus = kit.Status

const (
	TestPass = kit.StatusPass
	TestFail = kit.StatusFail
	TestSkip = kit.StatusSkip
)

// CheckResult is the engine's per-step result record; StepResult wraps it with the step's
// identity. Both live in kit (checkresult.go).
type CheckResult = kit.CheckResult

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
	// VmName is the per-deploy DOMAIN IDENTITY for a VM deployment — the deploy/bed name
	// (vmDomainIdentity), NOT the shared kind:vm entity. Box stays the deploy name (container +
	// DEPLOY_NAME identity); the operator-side libvirt/spice verbs must address the live libvirt
	// domain charly-<VmName>, so they read vmTargetName(). Empty for non-VM deployments, where
	// vmTargetName() falls back to Box.
	VmName string
	// Distros is the image's distro tag list (e.g. ["fedora:43", "fedora"]
	// or ["arch"]). Used by the `package:` verb's PackageMap resolution
	// to pick a distro-specific package name when names diverge across
	// distros (e.g. openssh-server on Fedora vs openssh on Arch).
	Distros []string

	// endpointCleanups holds the ssh -L forwards opened by cc.ResolveEndpoint (the generic
	// host-endpoint reverse-leg) DURING the current verb's Invoke; invokeVerbProvider closes
	// them AFTER the Invoke returns (the forward must outlive the plugin's dial). Per-Invoke.
	endpointCleanups []func()

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
// out-of-process vm/spice plugins as the libvirt-domain target: the per-deploy DOMAIN IDENTITY
// (VmName) when set, else the deploy name (Box). The plugin prefixes charly- onto whatever it
// receives and cannot LoadUnified to compute the domain itself, so the host threads the
// already-resolved domain identity through. A pod deployment leaves VmName empty, so its verbs
// correctly address charly-<deploy-name>.
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
// every check's outcome for CI ergonomics. The per-check walk (verb
// resolution, skip handling, variable expansion, venue swap, do-mode
// routing, the eventually: retry) is kit.RunOne — the check engine's plan
// walk lives in sdk/kit (planrun.go); this Runner is its host driver via
// the runnerPlanContext adapter (planrun_adapter.go).
func (r *Runner) Run(ctx context.Context, checks []Op) []CheckResult {
	pc := runnerPlanContext{r: r}
	results := make([]CheckResult, 0, len(checks))
	for i := range checks {
		results = append(results, kit.RunOne(ctx, pc, &checks[i]))
	}
	return results
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
