package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// yaml_setter.go — generic comment-preserving dot-path setter for
// charly.yml and the candy manifest. Used by `charly box set <dotpath> <value>` and
// `charly candy set <name> <dotpath> <value>` so the MCP tool surface can
// edit YAML config without a verb explosion (one verb per field) and
// without an "expose every yaml writer" anti-pattern.
//
// The dot-path syntax is intentionally narrow:
//
//   defaults.tag                    → top-level scalar
//   box.foo.base                 → nested scalar
//   box.foo.candy               → list (value parsed as YAML)
//   box.foo.port                → list (value parsed as YAML)
//
// The value is parsed as YAML, so:
//   charly box set defaults.tag auto         → string "auto"
//   charly box set box.foo.candy '[a,b]' → list of strings
//   charly box set box.foo.port '["8080:8080"]' → list with quoted item
//
// This deliberately does not try to model every candy manifest field as its
// own verb. For free-form auxiliary files (pixi.toml, root.yml, scripts),
// use `charly box write <path>` instead.

// SetByDotPath edits the file at path, navigating into the YAML
// structure via dotpath and replacing the leaf value with valueYAML
// (which is parsed as YAML so callers can pass scalars, lists, or maps).
// Comments and key order are preserved.
func SetByDotPath(path, dotpath, valueYAML string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	// Parse the value as YAML so callers can use lists, maps, scalars.
	var valueNode yaml.Node
	if err := yaml.Unmarshal([]byte(valueYAML), &valueNode); err != nil {
		return fmt.Errorf("parsing value %q as YAML: %w", valueYAML, err)
	}
	// yaml.Unmarshal wraps in a DocumentNode; peel it.
	leaf := &valueNode
	if leaf.Kind == yaml.DocumentNode && len(leaf.Content) > 0 {
		leaf = leaf.Content[0]
	}

	parts := strings.Split(dotpath, ".")
	if len(parts) == 0 || parts[0] == "" {
		return fmt.Errorf("empty dotpath")
	}
	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if err := setNodeByPath(doc, parts, leaf); err != nil {
		return fmt.Errorf("setting %s in %s: %w", dotpath, path, err)
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("marshalling %s: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// setNodeByPath walks parts through a mapping tree, creating intermediate
// mapping nodes as needed, and replaces the leaf with newValue. Returns an
// error if any intermediate node is not a mapping (e.g. you tried to
// descend into a scalar or list).
func setNodeByPath(node *yaml.Node, parts []string, newValue *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping at path segment %q, got kind %d", parts[0], node.Kind)
	}
	key := parts[0]
	rest := parts[1:]

	// Find existing child at key.
	var childIdx = -1
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			childIdx = i + 1
			break
		}
	}

	if len(rest) == 0 {
		// Leaf assignment.
		if childIdx >= 0 {
			node.Content[childIdx] = newValue
			return nil
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			newValue,
		)
		return nil
	}

	// Recurse into existing mapping or create one.
	if childIdx >= 0 {
		return setNodeByPath(node.Content[childIdx], rest, newValue)
	}
	newChild := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		newChild,
	)
	return setNodeByPath(newChild, rest, newValue)
}
