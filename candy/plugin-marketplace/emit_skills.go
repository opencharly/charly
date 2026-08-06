package marketplace

import (
	"fmt"
	"strings"

	"github.com/opencharly/spec/spec"
)

// emit_skills.go — the skill/agent synthesis: plugins/<family>/skills/<name>/SKILL.md +
// references/<stem>.md for `skill`-type entities, plugins/<family>/agents/<name>.md for
// `agent`-type entities. The SKILL.md frontmatter is exactly the two-key contract
// (name + description) the Skill tool + the corpus validator enforce, byte-zero (the header
// comment lands AFTER the frontmatter). references/ files carry no frontmatter by contract.

func emitSkills(em emissions, families []family) {
	for _, f := range families {
		for _, s := range f.Skills {
			if s.Type == "agent" {
				emitAgent(em, f.Name, s)
				continue
			}
			emitSkillMarkdown(em, f.Name, s)
		}
	}
}

func emitSkillMarkdown(em emissions, familyName string, s spec.Skill) {
	dir := "plugins/" + familyName + "/skills/" + s.Name
	front := skillFrontmatter(s.Name, s.Description)
	body := front + generatedHeaderBlock + strings.TrimSuffix(s.Content, "\n") + "\n"
	em[dir+"/SKILL.md"] = []byte(body)
	for _, r := range s.References {
		em[dir+"/references/"+r.Name+".md"] = []byte(generatedHeaderBlock + strings.TrimSuffix(r.Content, "\n") + "\n")
	}
}

func emitAgent(em emissions, familyName string, s spec.Skill) {
	model := s.Model
	if model == "" {
		model = "inherit"
	}
	rel := "plugins/" + familyName + "/agents/" + s.Name + ".md"
	front := fmt.Sprintf("---\nname: %s\n%s\nmodel: %s\n---\n\n", s.Name, descriptionFrontmatterLine(s.Description), model)
	em[rel] = []byte(front + generatedHeaderBlock + strings.TrimSuffix(s.Content, "\n") + "\n")
}

// skillFrontmatter emits the two-key frontmatter (name + description) the Skill tool + the
// corpus validator parse. The description may be MULTI-LINE (most cross-cutting skills carry a
// paragraph or a `|-` block in the source); a naive `description: %s` would produce invalid
// frontmatter the moment the value contains a newline. Emit it as a YAML block scalar instead.
func skillFrontmatter(name, description string) string {
	return fmt.Sprintf("---\nname: %s\n%s\n---\n\n", name, descriptionFrontmatterLine(description))
}

func descriptionFrontmatterLine(description string) string {
	body := strings.TrimSuffix(description, "\n")
	// ALWAYS a block scalar: a single-line description containing `: ` (e.g. "…involving:
	// building…") is invalid YAML inline, and a multi-line one needs the block form anyway.
	// `|` (clip) keeps a trailing newline in the parsed value, `|-` (strip) removes it — pick
	// the one matching the entity's value so the frontmatter ROUND-TRIPS exactly.
	indented := strings.ReplaceAll(body, "\n", "\n  ")
	if strings.HasSuffix(description, "\n") {
		return "description: |\n  " + indented
	}
	return "description: |-\n  " + indented
}
