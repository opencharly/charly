// The BUILT-IN `file` plugin's OWN CUE schema — the typed plugin_input for the `file`
// verb (a stat-based path probe in do:assert, a touch/cat+chmod file-creation in
// do:act). It is the SINGLE SOURCE for this plugin's params, used two ways (the same
// contract the reference exampleprobe and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `file` step's plugin_input against #FileInput.
//
// SELF-CONTAINED: it references NO base def — the `contains` matcher shape reproduces
// standalone under plugin-private def names (#FileContains / #FileMatcher /
// #FileMatchOp, so there is NO collision with the base #Matcher / #MatchOpMap when
// base ++ plugin compiles), so it compiles standalone (gengotypes +
// the load-gate compile) AND splices onto the base.
//
// `file` is DUAL-NATURED — a state-provision verb that is BOTH a CheckVerbProvider
// (RunVerb → r.runFile, the stat probe that keeps the live *Runner) AND a
// ProvisionActor (RenderProvisionScript → mkdir/touch/cat+chmod, the RUNTIME
// file-creation act, distinct from the BUILD-time COPY directives the write:/copy:
// verbs emit). The seven file-EXCLUSIVE fields (file/exists/owner/group_of/filetype/
// contains/sha256, read ONLY by the `file` verb) LEFT #Op for this def. `mode` is the
// shared companion: it STAYS in base #Op (the copy/write install verbs read Op.Mode at
// deploy) yet is reproduced here so the file plugin reads its own plugin_input.mode —
// exactly how `gid` is shared between unix_group and user. The probe asserts mode/owner/
// group_of/filetype/contains/sha256 when set; the act renders touch + `chmod <mode>`.
#FileInput: {
	// file — the absolute path the stat probe inspects (assert) / the file
	// touch/cat creates (act). The verb discriminator.
	file: string @go(File)
	// exists — whether the path is expected to exist (default true). Tri-state pointer
	// so an absent key means "expected to exist".
	exists?: bool @go(Exists,type=*bool)
	// mode — optional expected octal permission string (assert) / `chmod` arg (act).
	// The SHARED companion: also a base #Op modifier the copy/write verbs read.
	mode?: string & =~"^0[0-7]{3,4}$" @go(Mode)
	// owner — optional expected owning user name (stat %U).
	owner?: string @go(Owner)
	// group_of — optional expected owning group name (stat %G). Spelled group_of
	// because `group` is a reserved kind word (the getent-group verb is unix_group).
	group_of?: string @go(GroupOf)
	// filetype — optional expected node type (goss-parity short forms).
	filetype?: ("file" | "directory" | "symlink") @go(Filetype)
	// contains — optional goss-style matchers the file's contents must satisfy.
	// Reproduces the contains-default matcher-list shape standalone (scalar / single
	// operator-map / list); a bare scalar means `contains` at decode (decodeContainsList).
	contains?: #FileContains @go(Contains)
	// sha256 — optional expected SHA-256 hex digest of the file's contents.
	sha256?: string @go(Sha256)
}

// #FileContains is the contains-default matcher list: a single matcher OR a list.
#FileContains: (#FileMatcher | [...#FileMatcher])

// #FileMatcher mirrors the base #Matcher: a bare scalar (implicit match) or a
// single-operator map.
#FileMatcher: (string | bool | number | #FileMatchOp)

// #FileMatchOp mirrors the base #MatchOpMap: exactly one matcher operator key.
#FileMatchOp: {equals: _} | {not_equals: _} | {contains: _} | {not_contains: _} | {matches: _} | {not_matches: _} | {lt: _} | {le: _} | {gt: _} | {ge: _}
