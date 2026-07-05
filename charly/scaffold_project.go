package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/kit"
)

// scaffold_project.go — project-level authoring helpers used by the
// `charly box new project`, `charly box new box`, `charly box add-candy`, and
// `charly box rm-candy` commands. These exist primarily so the MCP tool
// surface can author a project from scratch over RPC, without the agent
// needing direct filesystem access.
//
// All YAML mutations go through the yaml.v3 *node* API so comments and
// key order are preserved across edits — re-marshalling parsed values
// would scramble human-edited charly.yml files.
//
// Schema v4 cutover (2026-05): every authoring verb defaults to
// charly.yml as the canonical root file. Legacy projects with a
// per-kind box.yml at the project root must run `charly migrate`
// first; the scaffolders error cleanly when charly.yml is missing.

// scaffoldCharlyYAML is the seed charly.yml written into a fresh project. The
// project is immediately usable — the default distro/builder/init/resource build
// vocabulary AND sidecar templates are embedded in the charly binary
// (charly/charly.yml), so there is no build vocabulary to copy or wire.
const scaffoldCharlyYAML = `# charly.yml — unified project root: the single file a project needs.
# See https://github.com/opencharly/charly for documentation.
#
# Box (image) and candy (layer) definitions are DISCOVERED per name:
#   box/<name>/charly.yml   — one box per directory
#   candy/<name>/charly.yml — one candy per directory
# The default distro/builder/init build vocabulary is EMBEDDED in the charly
# binary; declare distro:/builder:/init:/resource: here only to EXTEND or
# OVERRIDE it.
#
# Cross-kind name reuse is permitted — a single name (e.g. my-app) MAY exist
# simultaneously as a candy, a box, a pod, a vm, a k8s, a local, AND a deploy
# entry. charly verbs disambiguate by command context.

version: __SCHEMA_VERSION__

discover:
  - path: box
    recursive: true
  - path: candy
    recursive: true

defaults:
  registry: ghcr.io/example
  tag: auto
  platform:
    - linux/amd64
  build: [rpm]
`

// scaffoldGitignore keeps the build artefact dir + common scratch files
// out of git so a fresh project is committable as-is.
const scaffoldGitignore = `# Build artefacts
.build/

# Editor / OS
.DS_Store
*.swp
`

// ScaffoldProject creates an empty charly project at dir. Idempotency: errors
// out if dir already contains an charly.yml so we never silently
// clobber an existing project. The dir itself may exist.
func ScaffoldProject(dir string) error {
	if dir == "" {
		return fmt.Errorf("project directory must be specified")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating project directory: %w", err)
	}
	charlyPath := filepath.Join(dir, UnifiedFileName)
	if _, err := os.Stat(charlyPath); err == nil {
		return fmt.Errorf("charly.yml already exists at %s; refusing to overwrite", charlyPath)
	}
	seed := strings.ReplaceAll(scaffoldCharlyYAML, "__SCHEMA_VERSION__", LatestSchemaVersion().String())
	if err := os.WriteFile(charlyPath, []byte(seed), 0o644); err != nil {
		return fmt.Errorf("writing charly.yml: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, DefaultBoxDir), 0o755); err != nil {
		return fmt.Errorf("creating box/: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, DefaultCandyDir), 0o755); err != nil {
		return fmt.Errorf("creating candy/: %w", err)
	}
	gitignorePath := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(gitignorePath, []byte(scaffoldGitignore), 0o644); err != nil {
			return fmt.Errorf("writing .gitignore: %w", err)
		}
	}
	return nil
}

// AddBox writes a new box to its discovered per-box file box/<name>/charly.yml as
// a node-form IMAGE — `<name>: {candy: {base: …}}`. EDGE-INHERIT cutover D merged
// the `box:` KIND into `candy:`: an image is a `candy:` node carrying `base:` (its
// presence is what makes it an image, not a layer). The base argument is the value
// of the image's `base:` field (an external URL or the name of another box). If
// layers is non-nil it populates the image's `candy:` composition list. Errors if
// box/<name>/charly.yml exists.
func AddBox(dir, name, base string, layers []string) error {
	if name == "" {
		return fmt.Errorf("box name must be specified")
	}
	dest := filepath.Join(dir, DefaultBoxDir, name, UnifiedFileName)
	if fileExists(dest) {
		return fmt.Errorf("box %q already exists at %s", name, dest)
	}
	// The candy: value (the image body) — name is the NODE KEY, not a field.
	inner := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	inner.Content = append(inner.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "base"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: base},
	)
	if len(layers) > 0 {
		candiesNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, l := range layers {
			candiesNode.Content = append(candiesNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: l},
			)
		}
		inner.Content = append(inner.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "candy"},
			candiesNode,
		)
	}
	// node-form: <name>: {candy: <inner>}
	candyDisc := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	candyDisc.Content = append(candyDisc.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "candy"},
		inner,
	)
	wrapper := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	wrapper.Content = append(wrapper.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
		candyDisc,
	)
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{wrapper}}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating box directory: %w", err)
	}
	return saveYAMLNodeFile(dest, doc)
}

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
	return saveYAMLNodeFile(path, root)
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
	return saveYAMLNodeFile(path, root)
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

// saveYAMLNodeFile marshals a node tree back to an arbitrary file path,
// preserving comments + key order (the yaml.v3 Node round-trip). Used when an
// edit targets charly.yml itself or a per-kind import file.
func saveYAMLNodeFile(path string, root *yaml.Node) error {
	data, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshalling %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	return nil
}
