// schema/hook.cue — the SELF-CONTAINED CUE schema validating the `hook` KIND's authored VALUE
// (the harness gate scripts). Self-contained per the group/agent reproduction contract — the
// same authored wire keys as the core #Hook (spec/schema/hook.cue, the single source that
// generates spec.Hook); the host validates against #HookInput at load and this plugin's Invoke
// canonicalises through spec.Hook. trigger/matcher absent ⇒ an AUX file (gitcmd.py, gate_test.py)
// emitted to .claude/hooks/ but not wired into settings.json.
#HookInput: close({
	name:     string & =~"^[a-z][a-z0-9._-]*$" // file stem incl. extension (.sh/.py)
	content:  string & !=""                    // inline script (block scalar)
	trigger?: string & !=""                    // "PreToolUse" … (settings.json hooks.<trigger>)
	matcher?: string & !=""                    // "Bash" … (required iff trigger present)
	when?:    string & !=""                    // optional settings.json when clause
	mode?:    *"0755" | "0644"                 // emitted file mode (chmod +x for .sh by default)
})
