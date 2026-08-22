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
		version: "2026.204.1223"
		name:    "strip-deploy-shell-overlay"
		// the deploy-scope shell: overlay is authorable on a per-host
		// charly.yml deploy entry too (as well as a project charly.yml) —
		// touches_host so the per-host config is swept as well.
		touches_host: true
		apply:        "stripDeployShellOverlay"
	},
	{
		version: "2026.223.1018"
		name:    "k8s-to-kubernetes"
		// the deploy substrate kind `k8s:` → `kubernetes:` (full naming cleanup) and the
		// inner deploy-knobs block `kubernetes:` → `deploy:` (the outer-node rename
		// collides with the inner block, so the inner block becomes `deploy:`).
		// op 1 renames every `k8s:` discriminator key (deploy nodes + cluster templates);
		// op 2 renames the inner deploy-knobs block, scoped by under_kind: "kubernetes" so
		// it only matches `kubernetes:` keys nested inside a `kubernetes:` entity, never
		// the outer node. Order matters: op 2 needs the `kubernetes` kind to scope under.
		ops: [
			{op: "rename_key", from: "k8s", to: "kubernetes", scope: "any"},
			{op: "rename_key", from: "kubernetes", to: "deploy", scope: "any", under_kind: "kubernetes"},
		]
	},
	{
		version: "2026.225.1508"
		name:    "remove-candy-localpkg"
		// the candy-body `localpkg:` map (the OS-tracked package install) is REMOVED —
		// replaced by the `packaging:` section (the nFPM cutover). A candy carrying the
		// old field is a hard schema violation, so migrate deletes it. Candy-body field,
		// never authored on the per-host deploy overlay — no touches_host needed.
		ops: [
			{op: "delete_key", key: "localpkg", scope: "any", under_kind: "candy"},
		]
	},
	{
		version: "2026.232.0520"
		name:    "install-template-to-phases"
		// the legacy top-level `#Format.install_template` / `#Builder.install_template`
		// fields (the (install, container) fallback) are REMOVED — their content
		// migrates into `format.<fmt>.phase.install.container` / the builder equivalent,
		// the phase: block's single source of truth (strict-cleanup cutover, Unit 3b).
		// The nested move can't be expressed as rename_key/move_key ops (spec-side
		// version.cue), so a Go reshaper hook moves it. A project charly.yml carrying
		// the old field is a hard schema violation; the embedded build vocabulary is
		// migrated in-tree — no touches_host (the format/builder vocab is a project
		// charly.yml section, never a per-host deploy-overlay field).
		apply: "installTemplateToPhases"
	},
]
