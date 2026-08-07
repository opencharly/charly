// schema/skill.cue — the SELF-CONTAINED CUE schema validating the `skill` KIND's authored VALUE.
// Ships over Describe (schema_cue); references NO base def so it compiles standalone
// (BuildCapabilities compiles it alone, failing loudly if broken) AND splices onto the base
// (the base ++ plugin splice detects a def-name collision, not resolves base refs).
//
// It is a FAITHFUL reproduction of the core #Skill (spec/schema/skill.cue — the single source
// that generates spec.Skill via gengotypes), following the group/agent/… externalized-kind
// pattern: the SAME authored WIRE keys, so the host validates a real skill entity against
// #SkillInput (validateAuthoredPluginInput, the load gate) and this plugin's Invoke canonicalises
// the body back through the core spec.Skill type.
#SkillInput: close({
	name:        string & =~"^[a-z][a-z0-9-]*$" // marketplace-globally-unique skill id (SKILL.md folder name)
	family:      string & =~"^[a-z][a-z0-9-]*$" // marketplace family → plugins/<family>/
	owner:       string & =~"^[a-z][a-z0-9-]*$" // owning candy/concept-candy entity name
	description: string & !=""                   // SKILL.md frontmatter description (Skill-tool dispatch keyword)
	content:     string & !=""                   // SKILL.md markdown body
	type?:       *"skill" | "agent"              // "agent" ⇒ a sub-agent definition
	model?:      string & !=""                   // agent type: frontmatter model
	tools?:      [...(string & !="")]            // agent type: allowed tool set
	references?: [...#SkillReferenceInput]       // SKILL.md references/<stem>.md split files
	triggers?:   [...(string & !="")]            // R0-dispatcher trigger phrases
	category?:   *"development" | "commands" | "kind" | "images"
})
#SkillReferenceInput: close({
	name:    string & =~"^[a-z0-9][a-z0-9-]*$" // file stem (references/<name>.md)
	content: string & !=""
})
