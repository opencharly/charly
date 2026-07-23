// migrations.cue — the declarative migration table: the DATA the `charly migrate`
// engine interprets (embedded via //go:embed in engine.go). Each entry is validated
// at process start against #Migration (schema/migration.cue, beside this file in the
// plugin). Both the table DATA and the #Migration schema live HERE in
// candy/plugin-migrate, OUTSIDE the sdk schema, so neither enters the spec codegen /
// vocab concatenation — engine data + a plugin-only validation schema, not ingress.
//
// At the current migration-baseline reset the table is EMPTY: no config below the
// current schema HEAD is migratable (`charly migrate` stamps a current-format
// config to HEAD and refuses anything below #SchemaFloor). Add a future migration
// by appending ONE entry here and bumping #SchemaVersion in
// sdk/schema/version.cue, then `task cue:gen`. Common ops need zero new Go:
//
//   migrations: [
//     {version: "2026.200.0800", name: "widget-rename",
//      ops: [{op: "rename_key", from: "widget", to: "gadget", scope: "any"}]},
//   ]
//
// A structural reshape the ops can't express sets `apply: "<hook>"` and registers
// one Go hook in goHooks. See /charly-build:migrate.
migrations: [
	{
		version:      "2026.186.2323"
		name:         "compact-node-form"
		touches_host: true
		apply:        "compactNodeForm"
	},
	{
		version: "2026.202.0105"
		name:    "strip-candy-libvirt-field"
		// candy-level libvirt: is a candy-body field, never authored on the
		// per-host deploy overlay — no touches_host needed.
		apply: "stripCandyLibvirtField"
	},
	{
		version: "2026.203.2359"
		name:    "strip-deploy-shell-overlay"
		// the deploy-scope shell: overlay is authorable on a per-host
		// charly.yml deploy entry too (as well as a project charly.yml) —
		// touches_host so the per-host config is swept as well.
		touches_host: true
		apply:        "stripDeployShellOverlay"
	},
]
