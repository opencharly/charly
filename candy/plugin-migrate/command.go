package migrate

import (
	"fmt"
	"io"
	"os"

	"github.com/alecthomas/kong"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
)

// command.go — the `charly migrate` CLI grammar this plugin owns. runMigrateCLI backs
// BOTH placements: the compiled-in command:migrate Invoke(OpRun) (the operator path AND
// the refs.go remote-cache auto-migration) and the out-of-process cmd/serve CLI. It brings
// a current-format opencharly config up to the head schema CalVer via the declarative
// engine: at HEAD → no-op; in [floor, head) → apply the newer steps + re-stamp; below floor
// → refused (the historical chain was removed at the baseline reset).

// MigrateCmd is the `charly migrate` flag set.
type MigrateCmd struct {
	DryRun      bool   `long:"dry-run" help:"Print every change the chain would make without touching the filesystem"`
	ProjectOnly bool   `long:"project-only" help:"Migrate only the project dir, skipping the per-host deploy overlay (the remote-cache auto-migration path)"`
	Quiet       bool   `long:"quiet" help:"Suppress progress output (the auto-migration path)"`
	Dir         string `long:"dir" help:"Project directory holding charly.yml (default: the current working directory)"`
}

// runMigrateCLI parses args, builds the MigrateContext, and runs the engine. Returns the
// engine error so the caller (Invoke or CliMain) can propagate a non-zero exit — a below-floor
// refusal MUST fail, not silently pass.
func runMigrateCLI(args []string) error {
	var cmd MigrateCmd
	done, err := sdk.ParseInProcCLI("migrate", &cmd, args,
		kong.Description("Migrate any opencharly config up to the latest schema CalVer (single idempotent chain — no sub-verbs)"))
	if err != nil || done {
		return err
	}

	dir := cmd.Dir
	if dir == "" {
		if dir, err = os.Getwd(); err != nil {
			return err
		}
	}
	var out io.Writer = os.Stderr
	if cmd.Quiet {
		out = io.Discard
	}
	// project-only skips the per-host overlay entirely (never resolves DeployConfigPath); full
	// mode resolves the overlay so an operator `charly migrate` brings both up to head.
	hostDeployPath := ""
	if !cmd.ProjectOnly {
		if hostDeployPath, err = kit.DefaultDeployConfigPath(); err != nil {
			return err
		}
	}
	ctx := &MigrateContext{Dir: dir, HostDeployPath: hostDeployPath, DryRun: cmd.DryRun, Out: out}
	if _, err := runMigrations(ctx, cmd.ProjectOnly); err != nil {
		return err
	}
	if cmd.DryRun {
		fmt.Fprintln(os.Stderr, "(dry-run — no files were modified)")
	}
	return nil
}
