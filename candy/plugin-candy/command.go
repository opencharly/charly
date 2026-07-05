package candy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/kit"
)

// command.go — the externalized `charly candy` command (the candy-manifest authoring tree: set /
// add-{rpm,deb,pac,aur}). The plugin OWNS the ENTIRE logic — the subcommand grammar AND the
// comment-preserving yaml.Node mutation of candy/<name>/charly.yml. The only shared pieces are the
// GENERIC yaml utilities kit.SetByDotPath / kit.MappingChild (also used by `charly box set` /
// `charly box scaffold`); there is NO core candy logic and NO HostBuild seam — a plugin editing yaml
// owns that itself, so `charly candy` works identically compiled-in OR out-of-process.

var candyUsage = `usage: charly candy <set <name> <path> <value> | add-rpm|add-deb|add-pac|add-aur <name> <pkg…>>`

// sectionDistroPath maps an add-<fmt> section name to the `distro:` map path its packages land under:
// add-rpm→fedora, add-pac→arch, add-aur→arch.aur, add-deb→the shared `debian,ubuntu` compound.
var sectionDistroPath = map[string][]string{
	"rpm": {"distro", "fedora"},
	"deb": {"distro", "debian,ubuntu"},
	"pac": {"distro", "arch"},
	"aur": {"distro", "arch", "aur"},
}

// runCandyCLI dispatches the candy subcommand (the first token). set mutates a dot-path; add-<fmt>
// appends packages to the distro-map section its alias targets.
func runCandyCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", candyUsage)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Println(candyUsage)
		return nil
	case "set":
		if len(rest) != 3 {
			return fmt.Errorf("usage: charly candy set <name> <path> <value>")
		}
		return candySet(rest[0], rest[1], rest[2])
	case "add-rpm", "add-deb", "add-pac", "add-aur":
		if len(rest) < 2 {
			return fmt.Errorf("usage: charly candy %s <name> <pkg…>", sub)
		}
		return appendCandyPackages(rest[0], strings.TrimPrefix(sub, "add-"), rest[1:])
	default:
		return fmt.Errorf("unknown candy subcommand %q\n%s", sub, candyUsage)
	}
}

// candySet sets a dot-path value on candy/<name>/charly.yml, descending into the `candy:` body (candy
// manifests are kind-keyed under `candy:`), via the generic kit.SetByDotPath.
func candySet(name, path, value string) error {
	candyYml, err := candyManifestPath(name)
	if err != nil {
		return err
	}
	if path != "candy" && !strings.HasPrefix(path, "candy.") {
		path = "candy." + path
	}
	return kit.SetByDotPath(candyYml, path, value)
}

// appendCandyPackages reads candy/<name>/charly.yml, appends packages to the `distro:` map section the
// add-<fmt> command targets (creating parent mappings as needed), and writes back — preserving comments
// via the yaml.Node API.
func appendCandyPackages(name, section string, pkgs []string) error {
	if len(pkgs) == 0 {
		return fmt.Errorf("no packages specified")
	}
	path, ok := sectionDistroPath[section]
	if !ok {
		return fmt.Errorf("unknown package section %q", section)
	}
	candyYml, err := candyManifestPath(name)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(candyYml)
	if err != nil {
		return fmt.Errorf("reading %s: %w", candyYml, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parsing %s: %w", candyYml, err)
	}
	// Candy manifests are kind-keyed under `candy:`; packages live under the `distro:` map inside it.
	candy, err := candyBodyNode(&root)
	if err != nil {
		return fmt.Errorf("%s: %w", candyYml, err)
	}
	sectionNode := candy
	for _, key := range path {
		sectionNode = ensureMappingChild(sectionNode, key)
	}
	pkgsNode := kit.MappingChild(sectionNode, "package")
	if pkgsNode == nil {
		pkgsNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		sectionNode.Content = append(sectionNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "package"},
			pkgsNode,
		)
	} else if pkgsNode.Kind != yaml.SequenceNode {
		// Upgrade a scaffold `package:` null/scalar to a real sequence in place, preserving the
		// existing key+comment association for yaml.Marshal.
		pkgsNode.Kind = yaml.SequenceNode
		pkgsNode.Tag = "!!seq"
		pkgsNode.Value = ""
		pkgsNode.Content = nil
	}
	// Idempotent append: skip packages already present (and dedupe within this call).
	existing := make(map[string]bool, len(pkgsNode.Content))
	for _, n := range pkgsNode.Content {
		if n.Kind == yaml.ScalarNode {
			existing[n.Value] = true
		}
	}
	for _, p := range pkgs {
		if existing[p] {
			continue
		}
		existing[p] = true
		pkgsNode.Content = append(pkgsNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p},
		)
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("marshalling %s: %w", candyYml, err)
	}
	return os.WriteFile(candyYml, out, 0o644)
}

// candyManifestPath resolves candy/<name>/charly.yml under the project cwd, erroring if absent.
func candyManifestPath(name string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	candyYml := filepath.Join(dir, kit.DefaultCandyDir, name, kit.UnifiedFileName)
	if _, err := os.Stat(candyYml); err != nil {
		return "", fmt.Errorf("candy %q not found at %s", name, candyYml)
	}
	return candyYml, nil
}

// candyBodyNode returns the `candy:` body mapping of a kind-keyed candy manifest, synthesising the
// wrapper for an empty/scalar root.
func candyBodyNode(root *yaml.Node) (*yaml.Node, error) {
	doc := root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		doc.Kind = yaml.MappingNode
		doc.Tag = "!!map"
		doc.Content = nil
	}
	candy := kit.MappingChild(doc, "candy")
	if candy == nil {
		return nil, fmt.Errorf("not a kind-keyed candy manifest (no `candy:`)")
	}
	return candy, nil
}

// ensureMappingChild returns the named child mapping of m, creating an empty mapping (with key) when
// absent.
func ensureMappingChild(m *yaml.Node, key string) *yaml.Node {
	if child := kit.MappingChild(m, key); child != nil {
		return child
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		child,
	)
	return child
}
