// migrations.cue — the declarative migration table: the DATA the `charly migrate`
// engine interprets (embedded via //go:embed in charly/migrate_engine.go). Each
// entry is validated at process start against #Migration (charly/schema/
// migration.cue). This file lives OUTSIDE charly/schema/ so it never enters the
// spec codegen / vocab concatenation — it is engine data, not ingress schema.
//
// At the current migration-baseline reset the table is EMPTY: no config below the
// current schema HEAD is migratable (`charly migrate` stamps a current-format
// config to HEAD and refuses anything below #SchemaFloor). Add a future migration
// by appending ONE entry here and bumping #SchemaVersion in
// charly/schema/version.cue, then `task cue:gen`. Common ops need zero new Go:
//
//   migrations: [
//     {version: "2026.200.0800", name: "widget-rename",
//      ops: [{op: "rename_key", from: "widget", to: "gadget", scope: "any"}]},
//   ]
//
// A structural reshape the ops can't express sets `apply: "<hook>"` and registers
// one Go hook in goHooks. See /charly-build:migrate.
migrations: []
