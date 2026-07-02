package main

// scaffold_helpers.go — shared YAML-node helpers for the `charly candy …`
// authoring commands (add-rpm/deb/pac/aur, set). They agree on where a
// kind-keyed candy manifest's body lives so the editors never disagree (R3).

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// candyBodyNode returns the `candy:` body mapping of a kind-keyed candy
// manifest, synthesising the wrapper for an empty/scalar root. Shared by the
// candy authoring helpers (add-rpm/deb/pac/aur, set) so they agree on where
// the body lives.
func candyBodyNode(root *yaml.Node) (*yaml.Node, error) {
	doc := root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		// Empty file or scalar root — synthesise the candy wrapper.
		doc.Kind = yaml.MappingNode
		doc.Tag = "!!map"
		doc.Content = nil
	}
	candy := mappingChild(doc, "candy")
	if candy == nil {
		return nil, fmt.Errorf("not a kind-keyed candy manifest (no `candy:`)")
	}
	return candy, nil
}

// ensureMappingChild returns the named child mapping of m, creating an empty
// mapping (with key) when absent.
func ensureMappingChild(m *yaml.Node, key string) *yaml.Node {
	if child := mappingChild(m, key); child != nil {
		return child
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		child,
	)
	return child
}
