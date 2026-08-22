// CUE schema for the `hook` KIND — a first-class HARNESS hook entity (the migration of the
// superproject's .claude/hooks/* gate scripts into candy config). A `hook:` node is a sibling
// top-level entity in candy/charly-hooks/charly.yml, carrying the script CONTENT inline.
//
// This is the HARNESS hook (the Claude Code PreToolUse/PrePush git-discipline gates), DISTINCT
// from the candy lifecycle `hook:` field (#CandyHook {post_enable, pre_remove}): the kind word
// `hook` lives on the kind-word spine while the lifecycle field lives inside a `candy:` body —
// different namespaces, no parse conflict (the U0 spike exercises a file carrying both).
//
// trigger/matcher absent ⇒ an AUX file (e.g. gitcmd.py, gate_test.py) emitted to .claude/hooks/
// but not wired into settings.json. The generator enforces: matcher required iff trigger present.
#Hook: close({
	name:     string & =~"^[a-z][a-z0-9._-]*$" // file stem incl. extension (.sh/.py)
	content:  string & !=""                    // inline script (block scalar)
	trigger?: string & !=""                    // "PreToolUse" … (settings.json hooks.<trigger>)
	matcher?: string & !=""                    // "Bash" … (required iff trigger present)
	when?:    string & !=""                    // optional settings.json when clause
	mode?:    *"0755" | "0644"                 // emitted file mode (chmod +x for .sh by default)
})
