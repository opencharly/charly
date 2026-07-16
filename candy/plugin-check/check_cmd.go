package check

import (
	"fmt"
	"io"
	"os"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// check_cmd.go — the `charly check` command tree, externalized into candy/plugin-check (P12). Each
// leaf gathers its config + resolves its plan, then forwards a run to the host "check-run" seam and
// formats the returned []StepResult. (SKELETON: the box leaf is wired; live/run/feature + the harness
// subcommands are added as the full command tree + AI-harness relocate here.)

// CheckFailExitCode is the process exit code `charly check` returns when a check RAN to completion but
// one or more checks FAILED — distinct from 0 (all passed) and 1 (command/usage/infra error). Mirrors
// the goss/pytest 0/1/2 convention; the plugin maps CheckFailedError → sdk.ExitCodeError{Code: 2}.
const CheckFailExitCode = 2

// CheckSkippedExitCode (3) is emitted when a bed cannot run because a required HOST prerequisite is
// absent (a GPU resource whose vendor has no matching card). It is NOT a failure, so automation records
// a SKIP. The plugin maps CheckSkippedError → sdk.ExitCodeError{Code: 3}.
const CheckSkippedExitCode = 3

// CheckFailedError marks a check that ran but had failing checks. The plugin's Invoke maps it to
// sdk.ExitCodeError{Code: CheckFailExitCode} so the compiled-in command exits 2.
type CheckFailedError struct {
	Failed int    // number of failed checks (0 = aggregate/unknown)
	Msg    string // optional message override (e.g. a bed-level aggregate)
}

func (e *CheckFailedError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("%d check(s) failed", e.Failed)
}

// CheckSkippedError marks a bed skipped for a missing host prerequisite. The plugin's Invoke maps it
// to sdk.ExitCodeError{Code: CheckSkippedExitCode} so the compiled-in command exits 3.
type CheckSkippedError struct{ Msg string }

func (e *CheckSkippedError) Error() string { return e.Msg }

// CheckCmd is the unified `charly check` command tree. The box/live/feature leaves gather their
// config + resolve their plan, then forward a run to the host "check-run" seam and format the
// returned []StepResult. The Wave-2 additions (run / run-local / the AI-facing + management leaves)
// drive the R10 bed sequence + the AI-iteration harness over HostBuild("cli") + the "check-bed"
// session seam; each leaf's IMPLEMENTATION lives in its own file (run_cmd.go / runlocal.go /
// synccreds.go / ai_helpers.go / note_cmd.go / report_cmd.go / list_agent_cmd.go) so the leaf
// relocation stays file-disjoint from this shared grammar struct.
type CheckCmd struct {
	Box     CheckBoxCmd     `cmd:"" name:"box" help:"Pure-box check (disposable container, build-scope checks)"`
	Live    CheckLiveCmd    `cmd:"" name:"live" help:"Full-stack check against a running deployment"`
	Feature CheckFeatureCmd `cmd:"" name:"feature" help:"Run a running deployment's baked plan as acceptance tests; agent steps are agent-graded (Agent Driven Evaluation)"`

	// — Wave-2 additions (leaf implementations in their own files) —
	Run            CheckRunCmd       `cmd:"" name:"run" help:"Run a disposable check bed (R10 sequence) or an iterate: entity (AI loop)."`
	RunLocal       CheckRunLocalCmd  `cmd:"run-local" hidden:"" help:"In-target harness driver (set by the host)."`
	SyncCredential CheckSyncCredCmd  `cmd:"sync-credential" help:"Copy AI credentials into a score's target."`
	Scope          CheckScopeCmd     `cmd:"" name:"scope" help:"AI-facing: print the active iteration's scope.yml."`
	LastTag        CheckLastTagCmd   `cmd:"last-tag" help:"AI-facing: print the prior iteration's image tag."`
	SelfEvaluate   CheckSelfCheckCmd `cmd:"self-evaluate" help:"AI-facing: re-run the in-scope plan live."`
	List           CheckListRunsCmd  `cmd:"" name:"list" help:"List past runs under .check/."`
	Report         CheckReportCmd    `cmd:"" name:"report" help:"Print a past result-<calver>.yml."`
	Note           CheckNoteCmd      `cmd:"" name:"note" help:"Persistent NOTES.md memory (read/append)."`
	ListAgent      CheckListAgentCmd `cmd:"list-ai" help:"List configured agents."`
}

// CheckBoxCmd runs a pure-box check: a disposable container built from the image, build-scope steps
// only. The engine (ExtractMetadata → venue → RunPlan) runs host-side via the "check-run" seam; the
// plugin owns the CLI parse, the "Image:" header, and the formatting.
type CheckBoxCmd struct {
	Image  string `arg:"" help:"Image reference (full ref or short name resolved against local container storage)"`
	Format string `long:"format" default:"text" help:"Output format: text, json, tap, yaml"`
}

func (c *CheckBoxCmd) Run() error {
	reply, err := hostCheckRun(spec.CheckRunRequest{Mode: "box", Image: c.Image})
	if err != nil {
		return err
	}
	if reply.NoSteps {
		fmt.Fprintln(os.Stderr, "No plan steps defined for this image.")
		return nil
	}
	fmt.Fprintf(os.Stderr, "Image: %s\n", reply.Image)

	// YAML format emits the shape the benchmark scorer (ParseCharlyTestOutput) expects — the
	// header prints to stderr above, the scorer payload to stdout. Exact-match "yaml" (mirroring
	// the former in-core box leaf); any other value falls through to the reportSteps formats.
	if c.Format == "yaml" {
		return emitImageTestYAML(os.Stdout, reply.Image, "", reply.Steps)
	}

	reportSteps(os.Stderr, reply.Steps, c.Format)
	return failErrorFor(reply.Steps)
}

// failErrorFor returns the exit-classifying error for a set of check step results — the ONE
// classifier for every check verb (box/live/feature — R3). nil when none failed; a
// *CheckFailedError (→ exit 2, checks-failed) when any GENUINE check failed; or a plain infra
// error (→ exit 1, the host default) when the ONLY failures are podman container-SETUP infra
// failures (R44). An infra failure means the check command never ran (the probe container's
// mount/passwd-gen raced concurrent build churn), so it MUST NOT read as a checks-failed
// verdict — and, having survived the eventually.go bounded retry, it is reported loudly, never
// swallowed as a pass. A run with BOTH classes surfaces as checks-failed (2) — a real check
// failure dominates. Keyed on the kit infra marker (kit.IsContainerInfraResult).
func failErrorFor(results []kit.StepResult) error {
	checkFails, infraFails := kit.ClassifyStepFailures(results)
	if checkFails > 0 {
		return &CheckFailedError{Failed: checkFails}
	}
	if infraFails > 0 {
		return fmt.Errorf("%d container-setup infra failure%s: podman setup raced concurrent build churn and the check command never ran — an infra error, not a check verdict (see the INFRA step(s) above)",
			infraFails, kit.Plural(infraFails))
	}
	return nil
}

// reportSteps writes results in the requested format (the pass/fail tallying + exit
// classification is failErrorFor's, R44). It delegates to kit.ReportStepResults (P12a
// follow-up: dedupes what was a byte-identical format-selection switch duplicated here AND in
// charly/check_feature_run.go, R3), so the externalized output stays byte-identical to the
// former in-core path.
func reportSteps(w io.Writer, results []kit.StepResult, format string) {
	kit.ReportStepResults(w, results, format)
}
