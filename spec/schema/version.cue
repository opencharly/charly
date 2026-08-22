// version.cue — the HEAD schema version + the migration floor: the SINGLE source
// of truth for schema versioning. `task cue:gen` reads these via the CUE API and
// emits spec/version_gen.go (const SchemaVersion / SchemaFloor); the
// parsed HEAD lives in kit.LatestSchemaVersion() / kit.SchemaFloor(), which
// parse those generated consts. All three defs are @go(-) so `cue exp
// gengotypes` emits no stray Go type (the #CalVer pattern).
//
// The literals are validated by #CanonCalVer — a STRICT fixed-width YYYY.DDD.HHMM
// (4/3/4 digits), tighter than the loose #CalVer wire pattern in _common.cue — so
// a non-canonical literal fails schema COMPILATION here (fail-fast at gen time),
// matching exactly what the strict Go kit.ParseCalVer accepts. kit.MustCalVer at
// process start is the belt-and-braces Go-side backstop.

// A canonical, fixed-width CalVer: 4-digit year, 3-digit day-of-year, 4-digit HHMM.
#CanonCalVer: string & =~"^[0-9]{4}\\.[0-9]{3}\\.[0-9]{4}$" @go(-)

// #SchemaVersion is the HEAD schema CalVer — the version every current-format
// config is stamped to and the value the load-time gate requires. Bumped by the
// schema-compaction cutover (compact node grammar: collections inline in the kind
// value, steps as an unnamed `plan:` list, plugin-verb sugar replacing the
// plugin:/plugin_input: envelope, live-verb fields relocated to plugin input
// defs, box env as a map) — a format change on every authored wire surface,
// migrated by the single `apply:` reshaper hook in candy/plugin-migrate. Then
// bumped again by the candy-level `libvirt:` field removal (Cutover B unit
// 3+4, R5 claim-keyed sweep): the field had zero live Go consumers, migrated
// away by the `stripCandyLibvirtField` reshaper hook. Bumped again (K5-B/
// validation-correctness batch) by the deploy-scope `shell:` overlay field
// removal: #Deploy.shell (#DeployShellOverlay) was authorable but had ZERO
// live consumer — MergeDeployShell, its only would-be merge, never had a
// production call site anywhere in this repo's history — migrated away by
// the `stripDeployShellOverlay` reshaper hook. Bumped again by the
// k8s→kubernetes full naming cleanup (the deploy substrate kind + cluster-template
// kind + every #K8s* def + the derived target enum): an authored WIRE-key change
// (`k8s:` → `kubernetes:`, and the inner deploy-knobs block `kubernetes:` → `deploy:`),
// migrated by the two rename_key ops in candy/plugin-migrate/migrations.cue.
// Bumped again by the packaging-section cutover (the candy `localpkg:` map →
// `packaging?: #Packaging` section, the `local_pkg` source-build field removal,
// and the LocalPkgInstallStep IR → the download leg): an authored WIRE-key
// change on the candy surface (`localpkg:` → `packaging:`) + the deploy
// InstallStepView wire (pkgbuild_ref/project_dir → package_name/version),
// migrated by the remove_key/rename_key ops in candy/plugin-migrate/migrations.cue
// (the spec module is vendored in-repo — schema cutovers land in the same superproject
// PR; at the time of this cutover the spec module was still a separate submodule).
// Re-stamped to the merge-time CalVer by the fresh pr-validator.
#SchemaVersion: #CanonCalVer & "2026.225.1508" @go(-)

// #SchemaFloor is the OLDEST schema version `charly migrate` can migrate FROM. At
// the migration-baseline reset it EQUALS #SchemaVersion — the deleted 47-step chain
// was the only path from any older format, so nothing below the current HEAD is
// migratable. An ordinary future cutover bumps ONLY #SchemaVersion, widening the
// [floor, HEAD) migratable window; a future baseline reset raises the floor again.
#SchemaFloor: #CanonCalVer & "2026.174.1100" @go(-)
