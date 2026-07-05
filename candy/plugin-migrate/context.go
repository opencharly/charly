package migrate

// context.go — the migration runtime context. The declarative engine + op-walker + file-walk
// drivers are engine.go; the CLI grammar that builds a context and runs the engine is command.go
// (runMigrateCLI), which both the compiled-in command:migrate Invoke and the cmd/serve CLI call.

import "io"

// MigrateContext is the migration runtime context: the project dir, the per-host overlay path,
// the flags, and the progress writer.
type MigrateContext struct {
	Dir            string    // project directory (holds charly.yml)
	HostDeployPath string    // per-host deploy overlay (~/.config/charly/charly.yml); empty in project-only mode
	DryRun         bool      // print changes without touching the filesystem
	Out            io.Writer // progress (os.Stderr for `charly migrate`, io.Discard for the remote-cache auto-migration)
}
