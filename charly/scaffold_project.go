package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/kit"
)

// scaffold_project.go — the project-level authoring-EDIT helpers used by the
// `charly box add-candy` and `charly box rm-candy` commands (the create-side
// `charly box new project/box/candy` ENGINE now lives in sdk/kit — kit.ScaffoldProject /
// kit.AddBox / kit.ScaffoldCandy — shared with the command:box plugin, candy/plugin-box). These exist
// primarily so the MCP tool surface can author a project over RPC, without the agent needing
// direct filesystem access.
//
// All YAML mutations go through the yaml.v3 *node* API so comments and
// key order are preserved across edits — re-marshalling parsed values
// would scramble human-edited charly.yml files. The marshal-to-file step is
// kit.SaveYAMLNodeFile (shared with kit.AddBox, R3).

// AddCandyToBox appends a candy to an existing box's `candy:` list.
// Idempotent: if the candy is already in the list, this is a no-op. The box is
// resolved across the discovered box/<name>/charly.yml, charly.yml, AND any
// flat-imported per-kind file,
// and the edit is saved to the file where the box actually lives.
func AddCandyToBox(dir, image, layer string) error {
	root, imgNode, path, err := resolveBoxNodeFile(dir, image)
	if err != nil {
		return err
	}
	candiesNode := kit.MappingChild(imgNode, "candy")
	if candiesNode == nil {
		candiesNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		imgNode.Content = append(imgNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "candy"},
			candiesNode,
		)
	}
	for _, n := range candiesNode.Content {
		if n.Kind == yaml.ScalarNode && n.Value == layer {
			return nil
		}
	}
	candiesNode.Content = append(candiesNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: layer},
	)
	return kit.SaveYAMLNodeFile(path, root)
}

// RemoveCandyFromBox removes the named candy from a box's `candy:`
// list. Errors out if the box does not exist; succeeds silently if the
// candy is not present. The box is resolved across the discovered
// box/<name>/charly.yml, charly.yml, AND any flat-imported per-kind file, and
// the edit is saved to the file where the box actually lives.
func RemoveCandyFromBox(dir, image, layer string) error {
	root, imgNode, path, err := resolveBoxNodeFile(dir, image)
	if err != nil {
		return err
	}
	candiesNode := kit.MappingChild(imgNode, "candy")
	if candiesNode == nil {
		return nil
	}
	out := candiesNode.Content[:0]
	for _, n := range candiesNode.Content {
		if n.Kind == yaml.ScalarNode && n.Value == layer {
			continue
		}
		out = append(out, n)
	}
	candiesNode.Content = out
	return kit.SaveYAMLNodeFile(path, root)
}

// ---------------------------------------------------------------------------
// yaml.Node helpers — kept private to this file so the surface is small.

func loadCharlyYAMLNode(dir string) (*yaml.Node, error) {
	path := filepath.Join(dir, UnifiedFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("charly.yml not found in %s; run `charly box new project .` to scaffold one", dir)
		}
		return nil, fmt.Errorf("reading charly.yml: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing charly.yml: %w", err)
	}
	return &root, nil
}

// docContent returns the top-level mapping node of a parsed YAML document.
// yaml.Unmarshal returns a DocumentNode whose single Content entry is the
// root mapping — peel that wrapper for callers.
func docContent(root *yaml.Node) *yaml.Node {
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		return root.Content[0]
	}
	return root
}

// imageBodyNode returns the IMAGE BODY node for box `name` in a parsed
// node-form document — the value of the named entity's `candy:` discriminator.
// EDGE-INHERIT cutover D merged the `box:` KIND into `candy:`: an image is a
// `candy:` node carrying `base:`/`from:`, so the scaffold reads the `candy:`
// disc (its value is the image body: base/from + the `candy:` composition list).
// Returns nil if the named node is absent or carries no `candy:` mapping.
func imageBodyNode(root *yaml.Node, name string) *yaml.Node {
	entity := kit.MappingChild(docContent(root), name)
	if entity == nil {
		return nil
	}
	body := kit.MappingChild(entity, "candy")
	if body == nil || body.Kind != yaml.MappingNode {
		return nil
	}
	return body
}

// flatLocalImports returns the bare-string `import:` items that are LOCAL file
// refs (same-repo per-kind files such as box.yml) — NOT @github refs and NOT
// namespaced single-key-map imports. The authoring-edit verbs search these for
// a box defined outside charly.yml itself.
func flatLocalImports(root *yaml.Node) []string {
	doc := docContent(root)
	imp := kit.MappingChild(doc, "import")
	if imp == nil || imp.Kind != yaml.SequenceNode {
		return nil
	}
	var out []string
	for _, item := range imp.Content {
		if item.Kind == yaml.ScalarNode {
			ref := strings.TrimSpace(item.Value)
			if ref != "" && !strings.HasPrefix(ref, "@") {
				out = append(out, ref)
			}
		}
	}
	return out
}

// resolveBoxNodeFile finds the YAML file that DEFINES box `name` — the
// discovered box/<name>/charly.yml (the canonical location), else charly.yml
// itself, else one of its flat-imported local per-kind files — and returns that
// file's parsed node tree, the box's value node, and the file path. The
// authoring-edit verbs (add-candy/rm-candy) mutate + save that file, so they work
// on boxes wherever they live, not only those inlined in charly.yml.
func resolveBoxNodeFile(dir, name string) (*yaml.Node, *yaml.Node, string, error) {
	// Discovered per-box file box/<name>/charly.yml (the canonical location) — a
	// node-form `<name>: {candy: {base|from: …}}` IMAGE whose `candy:` value is
	// the image body (base/from + the candy composition list).
	boxFile := filepath.Join(dir, DefaultBoxDir, name, UnifiedFileName)
	if data, rerr := os.ReadFile(boxFile); rerr == nil {
		var froot yaml.Node
		if yaml.Unmarshal(data, &froot) == nil {
			if inner := imageBodyNode(&froot, name); inner != nil {
				return &froot, inner, boxFile, nil
			}
		}
	}
	charlyRoot, err := loadCharlyYAMLNode(dir)
	if err != nil {
		return nil, nil, "", err
	}
	if n := imageBodyNode(charlyRoot, name); n != nil {
		return charlyRoot, n, filepath.Join(dir, UnifiedFileName), nil
	}
	for _, ref := range flatLocalImports(charlyRoot) {
		p := filepath.Join(dir, ref)
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			continue
		}
		var froot yaml.Node
		if yaml.Unmarshal(data, &froot) != nil {
			continue
		}
		if n := imageBodyNode(&froot, name); n != nil {
			return &froot, n, p, nil
		}
	}
	return nil, nil, "", fmt.Errorf("box %q not found in charly.yml or its imported per-kind files", name)
}
