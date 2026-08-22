package spec

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// load_directives.go — the kind-blind config-loader document DIRECTIVES relocated from sdk/kit
// (loader_directives.go) with the UnifiedFile they are fields of (#55 Phase B): the `import:` field
// (ImportEntry/ImportList) and the `discover:` field (DiscoverConfig/ScanSpec), plus the canonical
// manifest-filename constant they default to. These are pure DATA carriers (custom YAML shapes, no
// mechanism), so they live in the types-only spec module where charly core, sdk/kit, sdk/loaderkit,
// and every loader-consuming plugin share ONE copy. The AnchorScanSpecs path-anchoring FUNCTION lives
// here too (#55 C3b) — its only non-loaderkit caller was MergeUnified, which itself relocated into
// this spec module, so it can no longer live in sdk/kit (spec cannot import sdk/kit); sdk/kit keeps a
// forwarder (var AnchorScanSpecs = spec.AnchorScanSpecs) for its remaining walk.go caller.

// UnifiedFileName is the ONE box/candy manifest filename. Canonical loader DATA; ScanSpec below
// defaults to it. (sdk/kit keeps the sibling layout constants DefaultBoxDir/DefaultCandyDir.)
const UnifiedFileName = "charly.yml"

// ProjectDirEnv / ProjectRepoEnv are the environment names that carry charly's PROJECT SCOPE — the
// two mutually-exclusive answers to "which project's charly.yml is this process operating on".
// Canonical loader DATA, sharing the home of the manifest filename they select: the charly CLI
// publishes them as the `-C/--dir` and `--repo` flag environments, and every module that spawns a
// charly child (sdk/deploykit's packaging child) must set or strip the SAME two names, so the
// contract lives in the one module both sides import rather than as a literal in each.
//
// They are the DESTINATION, not yet the whole story: literal spellings still exist in the charly
// CLI (charly/plugin_command_prescan.go, charly/main_dir_test.go, charly/main_repo_test.go,
// charly/plugin_command_prescan_repo_test.go) and are converted alongside their consumers in
// opencharly/charly#309. The Kong struct tags in charly/main.go are the one PERMANENT exception:
// a Go struct tag is a string literal in the type declaration and cannot reference a constant.
const (
	ProjectDirEnv  = "CHARLY_PROJECT_DIR"
	ProjectRepoEnv = "CHARLY_PROJECT_REPO"
)

// ChildProjectEnv returns inherited with BOTH project-scope variables removed, then sets
// ProjectDirEnv to projectDir when projectDir is non-empty. It is the one implementation of
// "hand a charly child process a project scope", shared by every module that spawns one.
//
// Both names must be handled together, which is the whole reason this is a function rather than
// two constants each caller filters by hand: they are MUTUALLY EXCLUSIVE in the CLI, so a caller
// that strips ProjectDirEnv and leaves an inherited ProjectRepoEnv makes the child exit on
// "--repo and --dir are mutually exclusive" instead of running. Getting one right and the other
// wrong fails at runtime, not at compile time.
//
// Stripping is the default because a child's project scope is almost never the parent's: the
// packaging child runs under the project owning the candy it packages, and the MCP server's
// forked charly must reach its own --repo default rather than inherit the caller's directory.
func ChildProjectEnv(inherited []string, projectDir string) []string {
	out := make([]string, 0, len(inherited)+1)
	for _, kv := range inherited {
		name, _, _ := strings.Cut(kv, "=")
		if name == ProjectDirEnv || name == ProjectRepoEnv {
			continue
		}
		out = append(out, kv)
	}
	if projectDir != "" {
		out = append(out, ProjectDirEnv+"="+projectDir)
	}
	return out
}

// ImportEntry is one parsed `import:` list item. A flat entry (Namespace == "")
// merges the referenced file into the current root namespace; a namespaced
// entry mounts the referenced project under Namespace.
type ImportEntry struct {
	Namespace string // "" = flat import into the current root namespace
	Ref       string // local path or `@host/org/repo[/sub/path]:version`
}

// ImportList is the `import:` field type. Custom YAML decoding accepts a list
// whose items are either a bare string (flat) or a single-key mapping
// `alias: ref` (namespaced child import).
type ImportList []ImportEntry

// UnmarshalYAML decodes the mixed-shape import list.
func (il *ImportList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("import: must be a list (got kind=%v)", node.Kind)
	}
	out := make(ImportList, 0, len(node.Content))
	for i, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			if item.Value == "" {
				return fmt.Errorf("import[%d]: empty ref", i)
			}
			out = append(out, ImportEntry{Ref: item.Value})
		case yaml.MappingNode:
			if len(item.Content) != 2 {
				return fmt.Errorf("import[%d]: a namespaced entry must be a single-key map `alias: ref`", i)
			}
			alias := item.Content[0].Value
			ref := item.Content[1].Value
			if alias == "" || ref == "" {
				return fmt.Errorf("import[%d]: namespaced entry needs both an alias and a ref", i)
			}
			out = append(out, ImportEntry{Namespace: alias, Ref: ref})
		default:
			return fmt.Errorf("import[%d]: each item must be a string ref or a single-key `alias: ref` map (got kind=%v)", i, item.Kind)
		}
	}
	*il = out
	return nil
}

// MarshalYAML emits each entry compactly: a flat entry as a scalar string, a
// namespaced entry as a single-key `alias: ref` map — the same shapes
// UnmarshalYAML accepts (round-trip safe; used by migrators that write configs).
func (il ImportList) MarshalYAML() (any, error) { //nolint:unparam // error return kept for interface/API stability
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, e := range il {
		if e.Namespace == "" {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: e.Ref})
			continue
		}
		seq.Content = append(seq.Content, &yaml.Node{
			Kind: yaml.MappingNode,
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: e.Namespace},
				{Kind: yaml.ScalarNode, Value: e.Ref},
			},
		})
	}
	return seq, nil
}

// DiscoverConfig is a FLAT list of generic scan specs. Each spec scans a path
// for directories containing its manifest; every discovered manifest is parsed
// as a multi-document stream and routed by SHAPE (the kind-key it carries), so
// one discover root can surface candies, boxes, deploys — any kind. There is no
// kind dimension and no hardcoded path/filename: discovery is fully configured
// in charly.yml.
type DiscoverConfig []ScanSpec

// ScanSpec describes one discovery root. Accepts string shorthand
// ("candy" → {Path: "candy", Recursive: true}) or the explicit object form
// ({path: X, recursive: false}). Empty Path is invalid.
type ScanSpec struct {
	Path      string `yaml:"path" json:"path"`
	Recursive bool   `yaml:"recursive" json:"recursive"`
	// Manifest is the per-directory manifest filename to look for. Empty
	// defaults to UnifiedFileName; configurable per spec in charly.yml.
	Manifest string `yaml:"manifest,omitempty" json:"manifest,omitempty"`
}

// UnmarshalYAML accepts the string shorthand where Recursive defaults to true,
// and the object form where Recursive defaults to true when omitted.
func (s *ScanSpec) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		s.Path = node.Value
		s.Recursive = true
		s.Manifest = UnifiedFileName
		return nil
	}
	// Object form — decode with `recursive` defaulting to true when absent.
	// yaml.v3 has no direct "default true"; we interpret missing as true by
	// looking at the raw node and only clearing Recursive when the field is
	// explicitly set to false.
	var raw struct {
		Path      string `yaml:"path" json:"path"`
		Recursive *bool  `yaml:"recursive" json:"recursive"`
		Manifest  string `yaml:"manifest" json:"manifest"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	s.Path = raw.Path
	if raw.Recursive == nil {
		s.Recursive = true
	} else {
		s.Recursive = *raw.Recursive
	}
	s.Manifest = raw.Manifest
	if s.Manifest == "" {
		s.Manifest = UnifiedFileName
	}
	return nil
}

// AnchorScanSpecs returns a copy of `specs` with every relative Path
// resolved to an absolute path against `srcDir`. Absolute paths are
// kept verbatim. Empty srcDir leaves specs unchanged so the
// root-file merge (called with rootDir == workspace) is a no-op.
// Path-anchoring MECHANISM (filepath) over spec.ScanSpec, relocated from
// sdk/kit (#55 C3b) so MergeUnified — which itself moved into this spec
// module — can call it without a spec→sdk import inversion.
func AnchorScanSpecs(specs []ScanSpec, srcDir string) []ScanSpec {
	if srcDir == "" || len(specs) == 0 {
		return specs
	}
	out := make([]ScanSpec, len(specs))
	for i, s := range specs {
		out[i] = s
		if s.Path != "" && !filepath.IsAbs(s.Path) {
			out[i].Path = filepath.Join(srcDir, s.Path)
		}
	}
	return out
}
