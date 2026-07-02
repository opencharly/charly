package main

// migrate.go — the in-core `charly migrate` context + entry points. The migration
// ENGINE (the CUE-anchored declarative table + the generic op-walker + the file-walk
// drivers) is migrate_engine.go; this file builds the MigrateContext and exposes the
// three entry points the command (migrate_cmd.go) and the remote-cache auto-migration
// (refs.go) call. The whole chain folded back into core at the migration-baseline
// reset — the former out-of-core migration module + its host-prelift shim were folded away.

import (
	"io"
	"os"
)

// MigrateContext is the migration runtime context: the project dir, the per-host
// overlay path, the flags, and the progress writer.
type MigrateContext struct {
	Dir            string    // project directory (holds charly.yml)
	HostDeployPath string    // per-host deploy overlay (~/.config/charly/charly.yml); empty in project-only mode
	DryRun         bool      // print changes without touching the filesystem
	Out            io.Writer // progress (os.Stderr for `charly migrate`, io.Discard for remote-cache auto-migration)
}

// NewMigrateContext builds a context for project dir, resolving the per-host overlay
// path. Progress goes to os.Stderr (the interactive `charly migrate` path).
func NewMigrateContext(dir string, dryRun bool) (*MigrateContext, error) {
	overlay, err := DeployConfigPath()
	if err != nil {
		return nil, err
	}
	return &MigrateContext{
		Dir:            dir,
		HostDeployPath: overlay,
		DryRun:         dryRun,
		Out:            os.Stderr,
	}, nil
}

// RunMigrations brings the project AND the per-host overlay up to the head schema.
// Returns whether anything changed. (Unchanged core API — migrate_cmd.go.)
func RunMigrations(ctx *MigrateContext) (bool, error) {
	return runMigrations(ctx, false)
}

// RunProjectMigrations brings only the project dir up to head — used by the remote-
// cache auto-migration (refs.go) so a remote fetch never mutates the user's per-host
// state. (Unchanged core API.)
func RunProjectMigrations(ctx *MigrateContext) (bool, error) {
	return runMigrations(ctx, true)
}
