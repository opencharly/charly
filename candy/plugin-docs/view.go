package docs

import (
	"fmt"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// The authored on-disk form is SHORTHAND-rich: `package: [git]` is a bare string list that
// canonicalizes to []PackageItem, a `command:` verb takes a scalar or a map, and so on. The
// canonicalizer for all of it (NormalizeEntityNode, charly/cue_normalize.go) is core-resident,
// and a plugin may import only the sdk + spec — never charly core — so it is out of reach here.
//
// Rather than transcribe a second copy of the candy schema, this file declares the NARROW
// PROJECTION the site actually renders: identity, prose, the package/service names, the plan's
// step intents, and the `plugin:` block. Every one of those is a scalar or a list of scalars in
// the authored form, so it decodes without any canonicalization at all. Fields whose authored
// shorthand needs the normalizer (the verb payloads, ports, tunnels) are deliberately NOT read —
// the site does not render them, and reading them would mean owning a shorthand expander here.
//
// spec types are still used wherever they decode cleanly (spec.Plugin, and spec.PackageNames for
// the canonical-form case), so this stays a projection of the schema, not a rival to it.

// candyView is the rendered subset of an authored candy.
type candyView struct {
	Version     string       `yaml:"version"`
	Description string       `yaml:"description"`
	Status      string       `yaml:"status"`
	Package     packageList  `yaml:"package"`
	Service     []serviceRef `yaml:"service"`
	Plan        []stepView   `yaml:"plan"`
	Plugin      *spec.Plugin `yaml:"plugin"`
	Base        string       `yaml:"base"`
	From        string       `yaml:"from"`
}

// boxView is the rendered subset of an authored box (a candy: node carrying base:/from:).
type boxView struct {
	Version     string   `yaml:"version"`
	Description string   `yaml:"description"`
	Enabled     *bool    `yaml:"enabled"`
	Base        string   `yaml:"base"`
	From        string   `yaml:"from"`
	Candy       []string `yaml:"candy"`
}

// serviceRef reads only a service entry's name.
type serviceRef struct {
	Name string `yaml:"name"`
}

// stepView reads a plan step's INTENT KEYWORD and its prose — the acceptance spec a reader cares
// about. The verb payload beside it is shorthand-rich and deliberately left unread.
type stepView struct {
	Run        string `yaml:"run"`
	Check      string `yaml:"check"`
	AgentRun   string `yaml:"agent-run"`
	AgentCheck string `yaml:"agent-check"`
	Include    string `yaml:"include"`
}

// Intent returns the step's keyword and prose.
func (s stepView) Intent() (string, string) {
	switch {
	case s.Check != "":
		return "check", s.Check
	case s.Run != "":
		return "run", s.Run
	case s.AgentCheck != "":
		return "agent-check", s.AgentCheck
	case s.AgentRun != "":
		return "agent-run", s.AgentRun
	case s.Include != "":
		return "include", s.Include
	}
	return "", ""
}

// packageList accepts BOTH authored forms of `package:` — the bare string list every candy in
// the tree actually writes, and the canonical {name, description} list the CUE schema defines.
type packageList []spec.PackageItem

func (p *packageList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			*p = append(*p, spec.PackageItem{Name: item.Value})
		case yaml.MappingNode:
			var pi spec.PackageItem
			if err := item.Decode(&pi); err != nil {
				return fmt.Errorf("decode package entry: %w", err)
			}
			*p = append(*p, pi)
		}
	}
	return nil
}

// Names returns the package names, reusing spec's canonical helper.
func (p packageList) Names() []string { return spec.PackageNames(p) }
