package spec

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// node_helpers.go — pure, kind-blind yaml.v3 node helpers, relocated from
// sdk/kit (FLOOR-SLIM axis-A mechanical batch) so charly core can reach them
// without importing a mechanism kit: charly's cue_normalize.go / materialize.go
// / node_build.go called kit.ScalarNode / kit.ClassifyDoc / kit.MappingRoot for
// zero mechanism reason — each is a tiny, self-contained node construction or
// document-shape classification with no registry/host coupling. kit.ScalarNode
// / kit.ClassifyDoc / kit.MappingRoot / kit.DocShape now alias these directly
// (zero logic change, zero call-site churn outside the 8 charly files that
// dropped their kit import).

// ScalarNode builds a string scalar YAML node.
func ScalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// MappingRoot unwraps a YAML document node to its top-level mapping node (or nil). Shared by
// charly core and the out-of-module candy/plugin-migrate engine (R3).
func MappingRoot(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// DocShape classifies one parsed YAML document's top level.
type DocShape int

const (
	// DocShapeEmpty — a scalar-null / empty mapping document (nothing to load).
	DocShapeEmpty DocShape = iota
	// DocShapeNode — the unified name-first node-form: reserved document
	// directives (version/import/discover/defaults/repo/provides) plus a flat
	// map of arbitrary-name entity nodes (each `<name>: {<discriminator>: …}`),
	// and NO top-level kind-map key. The ONE authoring surface; a legacy
	// kind-keyed / root-shape document is hard-rejected downstream (the host's
	// #NodeDoc CUE gate), not here.
	DocShapeNode
)

// ClassifyDoc inspects a document's top level and returns its shape: a non-empty
// mapping is a node-form document (arbitrary entity-name nodes and/or the reserved
// directives version/import/discover/…); a scalar-null / empty mapping is
// DocShapeEmpty; a non-mapping top level is an error. Entity + directive validation
// happens downstream (the loader parse + the #NodeDoc CUE gate).
func ClassifyDoc(node *yaml.Node) (DocShape, error) {
	if node == nil || node.Kind == 0 {
		return DocShapeEmpty, nil
	}
	// yaml.NewDecoder wraps content in a DocumentNode.
	inner := node
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return DocShapeEmpty, nil
		}
		inner = node.Content[0]
	}
	if inner.Kind == yaml.ScalarNode && inner.Tag == "!!null" {
		return DocShapeEmpty, nil
	}
	if inner.Kind != yaml.MappingNode {
		return 0, fmt.Errorf("top-level must be a mapping, got kind=%v", inner.Kind)
	}
	if len(inner.Content) == 0 {
		return DocShapeEmpty, nil
	}

	// A non-empty top-level mapping is a node-form document (arbitrary entity-name
	// nodes and/or the reserved directives version/import/discover/…). The bilingual
	// legacy-kind-map reader was deleted; entity + directive validation is downstream
	// (the loader parse + the #NodeDoc CUE gate).
	return DocShapeNode, nil
}
