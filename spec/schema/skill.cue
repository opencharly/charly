// CUE schema for the `skill` KIND — a first-class harness skill entity (the migration of the
// plugins/ skill corpus into candy configs). A `skill:` node is a sibling top-level entity in a
// candy's charly.yml (ParseDoc iterates every top-level node, each with its own single kind
// discriminator), so a candy file stacks its `candy:` node plus the `skill:` nodes it owns.
//
// The FULL skill definition is INLINE: every metadata field plus the markdown content as a block
// scalar — nothing is fetched from elsewhere. The marketplace generator (candy/plugin-marketplace,
// command:marketplace) synthesizes plugins/<family>/skills/<name>/SKILL.md + references/*.md from
// these entities; CollectSkills (sdk/deploykit) projects the composed candies' skills into the
// `ai.opencharly.skill` OCI label so a built image is self-describing (readable via
// `charly box labels` / `charly fleet from-box`, no external fetch).
//
// owner is the candy/concept-candy entity name that owns this skill — the build-time association
// that decides which images carry it (CollectSkills filters by owner ∈ composed candy chain).
// family is the marketplace family (the plugins/ directory name) the generator groups by.
//
// type: "agent" marks a sub-agent definition (the former plugins/<family>/agents/*.md) — one
// unified doc kind, same lifecycle; model/tools carry the agent frontmatter fields.
#Skill: close({
	name:        string & =~"^[a-z][a-z0-9-]*$" // marketplace-globally-unique skill id (SKILL.md folder name)
	family:      string & =~"^[a-z][a-z0-9-]*$" // marketplace family → plugins/<family>/
	owner:       string & =~"^[a-z][a-z0-9-]*$" // owning candy/concept-candy entity name
	description: string & !=""                   // SKILL.md frontmatter description (Skill-tool dispatch keyword)
	content:     string & !=""                   // SKILL.md markdown body (block scalar)
	type?:       *"skill" | "agent"              // "agent" ⇒ a sub-agent definition (frontmatter adds model/tools)
	model?:      string & !=""                   // agent type: frontmatter model (default "inherit")
	tools?:      [...(string & !="")]            // agent type: allowed tool set
	references?: [...#SkillReference]            // SKILL.md references/<stem>.md split files
	triggers?:   [...(string & !="")]            // R0-dispatcher trigger phrases (generated dispatcher table rows)
	category?:   *"development" | "commands" | "kind" | "images"
})
#SkillReference: close({
	name:    string & =~"^[a-z0-9][a-z0-9-]*$" // file stem (references/<name>.md)
	content: string & !=""
})
