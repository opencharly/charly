package marketplace

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// frontmatter_test.go — locks the SKILL.md frontmatter emission: a MULTI-LINE description (the
// norm for cross-cutting skills — a paragraph or a |- block in the source) must produce VALID
// YAML frontmatter that parses back to the exact name/description, and the content must survive
// the header + frontmatter intact.

func TestMultiLineDescriptionFrontmatterIsValidYAML(t *testing.T) {
	em := emissions{}
	fams := []family{{
		Name: "core",
		Meta: spec.MarketplaceFamily{Category: "commands"},
		Skills: []spec.Skill{{
			Name:        "ssh",
			Family:      "core",
			Owner:       "charly-core",
			Description: "Open an interactive shell into a pod.\nHandles agent forwarding and key setup.",
			Content:     "# ssh\nbody",
		}},
	}}
	emitSkills(em, fams)
	raw := string(em["plugins/core/skills/ssh/SKILL.md"])

	// frontmatter is everything before the generated header (byte-zero).
	idx := strings.Index(raw, generatedHeader)
	if idx < 0 {
		t.Fatalf("no generated header:\n%s", raw)
	}
	fm := map[string]string{}
	if err := yaml.Unmarshal([]byte(raw[:idx]), &fm); err != nil {
		t.Fatalf("frontmatter must parse as YAML: %v\n%s", err, raw[:idx])
	}
	if fm["name"] != "ssh" || fm["description"] != "Open an interactive shell into a pod.\nHandles agent forwarding and key setup." {
		t.Fatalf("frontmatter values wrong: %v", fm)
	}
	// content survives after the header.
	after := raw[idx+len(generatedHeader):]
	if !strings.Contains(after, "# ssh") || !strings.Contains(after, "body") {
		t.Fatalf("content lost after header:\n%s", after)
	}
}
