package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/alecthomas/kong"
)

// command.go is the command:feature leg of this plugin — the externalized `charly feature …`
// CLI, ported OUT of charly's core (the deleted charly/description_cmd.go's FeatureCmd +
// charly/plugin_command_feature.go) so the operator-facing feature command no longer links
// into the core binary. It owns the ENTIRE `charly feature` grammar verbatim from the former
// core command (list / pending / validate) — the leaf structs parse exactly as before; only
// each leaf's RUN body changed: it no longer calls the in-core loader + plan model directly
// (the unified loader LoadConfig/ScanCandy, the Step plan model, and validatePlanSteps — which
// is SHARED with `charly box validate` — all STAY core, R3). Instead it SHELLS BACK through
// three NEW HIDDEN core commands that do the loading + inspection in-core (the SAME
// `charly __cli-model` / `charly __plugin-providers` / `charly __preempt-status`
// internal-command pattern, the CLI being the only operational interface, R4):
//
//   - `charly feature list [kind]`        → `charly __feature-list [kind]`
//     (LoadConfig/ScanCandy + per-entity description summary + plan-step counts)
//   - `charly feature pending [entity]`   → `charly __feature-pending [entity]`
//     (the agent-graded plan steps — agent-run:/agent-check:)
//   - `charly feature validate [entity]`  → `charly __feature-validate [entity]`
//     (validatePlanSteps over every plan: block; the success line + non-zero exit on errors)
//
// Each shell-back syscall.Exec's the SAME charly binary that dispatched this plugin (CHARLY_BIN,
// stamped by the dispatch seam), so the in-core loader runs in the re-entered charly process
// and its stdout/stderr/exit-code flow back through this plugin (which IS the operator's
// `charly feature` process) natively. No core symbol crosses the process boundary.
//
// (The Feature RUN verbs are NOT part of this plugin — `charly box feature run` (image.go) and
// `charly check feature run` (check_cmd.go) stay children of box/check in the core binary.)
//
// Dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly feature <args…>`, charly RESOLVES this plugin's binary (host-built from source, or
// baked into /usr/lib/charly/plugins via pkg/arch) and syscall.Exec's it with the pass-through
// tokens after the `feature` word, in CLI mode (the go-plugin handshake cookie is stripped, so
// sdk.Main runs cliMain instead of serving gRPC) with CHARLY_BIN stamped to charly's own
// executable.

// cliMain is the CLI-mode entry point (sdk.Main calls it when charly fork/exec'd this plugin as
// a command passthrough). It parses the pass-through tokens against FeatureCmd and dispatches
// the selected subcommand via kctx.Run() (each leaf carries its own Run handler). Returns the
// process exit code.
func cliMain(args []string) int {
	var grp FeatureCmd
	parser, err := kong.New(&grp,
		kong.Name("feature"),
		kong.Description("charly feature — inspect plan-shaped descriptions: list/pending/validate"),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin-feature: build kong parser: %v\n", err)
		return 1
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin-feature: parse `charly feature %v`: %v\n", args, err)
		return 1
	}
	if err := kctx.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "charly feature: %v\n", err)
		return 1
	}
	return 0
}

// charlyBin returns the host charly binary the dispatch seam stamped into CHARLY_BIN, falling
// back to `charly` on PATH (e.g. if the plugin binary is run directly, off the dispatch path).
func charlyBin() string {
	if b := os.Getenv("CHARLY_BIN"); b != "" {
		return b
	}
	return "charly"
}

// execCharly REPLACES this process with `charly <hiddenCmd> [args…]` via syscall.Exec, so the
// re-entered charly runs the in-core loader + plan model and its stdout/stderr/exit-code flow
// back through this plugin (which IS the operator's `charly feature` process) natively. On
// success this never returns; only a PRE-exec failure (binary missing) returns an error.
func execCharly(args ...string) error {
	bin, err := exec.LookPath(charlyBin())
	if err != nil {
		return fmt.Errorf("resolving charly binary %q: %w", charlyBin(), err)
	}
	argv := append([]string{"charly"}, args...)
	if err := syscall.Exec(bin, argv, os.Environ()); err != nil { //nolint:gosec // bin is CHARLY_BIN, args are fixed hidden-command tokens
		return fmt.Errorf("exec %s: %w", bin, err)
	}
	return nil // unreachable: syscall.Exec replaced the process image
}

// FeatureCmd groups the `charly feature` authoring + inspection verbs. The leaf structs are
// byte-identical to the former core command (charly/description_cmd.go's FeatureCmd) so
// `charly feature …` parses exactly as before; the implementation moved to the hidden core
// shell-backs. (The Feature RUN verbs — `charly box feature run` / `charly check feature run` —
// stay children of box/check in the core binary, so they are NOT part of this grammar.)
type FeatureCmd struct {
	List     FeatureListCmd     `cmd:"list"     help:"Enumerate every kind: entity and its plan: steps"`
	Pending  FeaturePendingCmd  `cmd:"pending"  help:"List agent-graded plan steps (agent-run:/agent-check:)"`
	Validate FeatureValidateCmd `cmd:"validate" help:"Parse + binding consistency check for plan: blocks (called by charly box validate)"`
}

// FeatureListCmd: `charly feature list [<kind>]`. Shells back to `charly __feature-list`, which
// walks the resolved project config in-core and prints each entity's description summary.
type FeatureListCmd struct {
	Kind string `arg:"" optional:"" help:"Restrict to one kind (candy|box). Default: all."`
}

func (c *FeatureListCmd) Run() error {
	if c.Kind == "" {
		return execCharly("__feature-list")
	}
	return execCharly("__feature-list", c.Kind)
}

// FeaturePendingCmd: `charly feature pending [<entity>]`. Shells back to
// `charly __feature-pending`, which lists the agent-graded plan steps in-core.
type FeaturePendingCmd struct {
	Entity string `arg:"" optional:"" help:"Entity identifier (e.g. candy:redis); default: all"`
}

func (c *FeaturePendingCmd) Run() error {
	if c.Entity == "" {
		return execCharly("__feature-pending")
	}
	return execCharly("__feature-pending", c.Entity)
}

// FeatureValidateCmd: `charly feature validate [<entity>]`. Shells back to
// `charly __feature-validate`, which parses every plan: block in-core (via the shared
// validatePlanSteps) and exits non-zero on any validation error.
type FeatureValidateCmd struct {
	Entity string `arg:"" optional:"" help:"Entity identifier (e.g. candy:redis); default: all"`
}

func (c *FeatureValidateCmd) Run() error {
	if c.Entity == "" {
		return execCharly("__feature-validate")
	}
	return execCharly("__feature-validate", c.Entity)
}
