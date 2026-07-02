package main

import (
	"fmt"
	"os"
)

// MigrateCmd is `charly migrate` — the single, idempotent command that brings a
// current-format opencharly config up to the head schema CalVer. It runs the
// CUE-anchored declarative migration engine (migrate_engine.go): a config already at
// head is a no-op, a config in the migratable [floor, head) window has the newer
// declarative steps applied and is re-stamped to head, and a config below the floor
// is refused (the historical migration chain was removed at the baseline reset, so
// older formats are unmigratable). A future cutover adds one entry to
// charly/migrations.cue (+ a #SchemaVersion bump); the operator command never changes.
//
// The project directory is taken from the current working directory; use the
// top-level `-C` / `--dir` / CHARLY_PROJECT_DIR global to point at a different
// project (main() chdir's before dispatch, so os.Getwd already reflects it).
type MigrateCmd struct {
	DryRun bool `long:"dry-run" help:"Print every change the chain would make without touching the filesystem"`
}

func (c *MigrateCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	ctx, err := NewMigrateContext(dir, c.DryRun)
	if err != nil {
		return err
	}
	if _, err := RunMigrations(ctx); err != nil {
		return err
	}
	if c.DryRun {
		fmt.Fprintln(os.Stderr, "(dry-run — no files were modified)")
	}
	return nil
}
