// version.cue — the HEAD schema version + the migration floor: the SINGLE source
// of truth for schema versioning. `task cue:gen` reads these via the CUE API and
// emits charly/spec/version_gen.go (const SchemaVersion / SchemaFloor); the
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
// config is stamped to and the value the load-time gate requires. Unchanged by the
// migration-baseline reset (this cutover replaced the migration MACHINERY, not the
// ingress YAML format — no authored wire key changed), per /charly-build:migrate
// "a pure refactor that leaves every authored wire key untouched needs NO bump".
#SchemaVersion: #CanonCalVer & "2026.174.1100" @go(-)

// #SchemaFloor is the OLDEST schema version `charly migrate` can migrate FROM. At
// the migration-baseline reset it EQUALS #SchemaVersion — the deleted 47-step chain
// was the only path from any older format, so nothing below the current HEAD is
// migratable. An ordinary future cutover bumps ONLY #SchemaVersion, widening the
// [floor, HEAD) migratable window; a future baseline reset raises the floor again.
#SchemaFloor: #CanonCalVer & "2026.174.1100" @go(-)
