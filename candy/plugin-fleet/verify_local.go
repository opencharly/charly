package fleet

// verify_local.go — the `--verify` deploy-scope check pass for a non-lifecycle (in-place)
// external substrate, plugin-side (#55 W3 B3, relocated from charly/check_cmd.go's
// checkLocalDeployScope/runLocalDeployScopePlan/dispatchVerifyChecks). handleDeployApply
// (deploy_target.go) already decodes opts.Verify + req.HasLifecycle off the SAME wire every Add
// dispatch carries (spec.LifecycleOptsFromEmit already threads Verify), so this was reachable
// without any new wire field — the former core "STAYS core-side... out of this cutover's scope"
// framing (deploy_target.go's OLD header) was itself a deferral, not a genuine boundary finding.
//
// The template-plan lookup (the former core-only findLocalSpec, LoadUnified-coupled) needs NO new
// seam at all: this package's OWN node_resolve.go already carries lookupLocalTemplate, a fully
// plugin-native resolver — it fetches the "resolved-project" envelope via
// InvokeProvider("build","project",OpResolve,...) (no LoadUnified) and resolves the raw template
// body via a DIRECT InvokeProvider("kind","local",OpResolve,...) peer-dispatch call. Reusing it
// here (R3 — one local-template resolver, not a second) is what finally makes findLocalSpec/
// resolveLocalRefFor/namespace.go fully dead in core (deleted alongside this, super leg).
//
// Reaching command:check for the check pass itself runs via the SAME direct
// InvokeProvider(command,"check") pattern this file's sibling deploy_target.go already uses for
// the `deploy` class (lifecycleInvoke): command:check's own verifyChecksForHost
// (candy/plugin-check/verify_checks.go) re-materializes its executor PURELY from the request
// body's Venue field — no core-private reverse-channel plumbing is needed from the caller,
// confirming the former dispatchVerifyChecks's in-proc executor threading was redundant with the
// already-sanctioned wire shape.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/report"
	"github.com/opencharly/spec/spec"
)

// verifyLocalDeployScope runs the --verify deploy-scope check pass after a successful Add of a
// non-lifecycle (in-place) external substrate. Returns the failure count (0 = pass). Mirrors the
// former core checkLocalDeployScope's exact behavior (report to stdout, "text" format — the ONE
// format its sole caller, unified_targets.go's Add, ever used).
func verifyLocalDeployScope(ctx context.Context, exec *sdk.Executor, dir, name, from string, node *spec.Deploy, venueDesc spec.VenueDescriptor) (int, error) {
	plan, err := localDeployScopePlan(from, node)
	if err != nil {
		return 0, err
	}
	if len(plan) == 0 {
		fmt.Println("No plan steps to run.")
		return 0, nil
	}
	req := spec.VerifyChecksRequest{Plan: plan, Mode: "live", Box: name, VerifyOnly: true, Dir: dir, Venue: venueDesc}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("verify-checks: marshal request: %w", err)
	}
	out, err := exec.InvokeProvider(ctx, "command", "check", sdk.OpVerifyChecks, reqJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return 0, fmt.Errorf("verify-checks: command:check plugin: %w", err)
	}
	var results []spec.StepResult
	if len(out) > 0 {
		if err := json.Unmarshal(out, &results); err != nil {
			return 0, fmt.Errorf("verify-checks: decode reply: %w", err)
		}
	}
	return report.ReportStepResultsCount(os.Stdout, results, "text"), nil
}

// localDeployScopePlan collects a local deployment's deploy-scope plan — the kind:local template
// `check:` (base, resolved via node_resolve.go's lookupLocalTemplate) + the deploy node's own
// `check:` (extends/overrides) — mirroring the former core runLocalDeployScopePlan's assembly
// exactly. The per-host charly.yml overlay merge already runs INSIDE command:check's own
// verifyChecksRunPlan (it reads the per-host deploy config via sdk/deploykit itself), so this
// function's job is exactly the base-plan ASSEMBLY the former core function did — nothing more.
// Best-effort on the template lookup (a resolve failure logs to stderr, matching findLocalSpec's
// own former nil-on-absent semantics — a genuinely missing template already failed earlier, at
// Add's own substrate dispatch).
func localDeployScopePlan(from string, node *spec.Deploy) ([]spec.Step, error) {
	var plan []spec.Step
	if name := strings.TrimSpace(from); name != "" {
		rsvd, err := lookupLocalTemplate(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: --verify: resolve local template %q: %v\n", name, err)
		} else if rsvd != nil {
			plan = append(plan, rsvd.Plan...)
		}
	}
	if node != nil {
		plan = append(plan, node.Plan...)
	}
	return plan, nil
}
