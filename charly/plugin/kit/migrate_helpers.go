package kit

// migrate_helpers.go — small generic yaml.v3 / path helpers shared by charly core
// AND out-of-tree plugin candies via the importable kit (a separate module that
// cannot import package main). Core aliases each via `var x = kit.X` (so core call sites are unchanged);
// the candy aliases them in aliases.go. ONE copy each (R3).

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/charly/charly/spec"
	"gopkg.in/yaml.v3"
)

// FileExists reports whether path exists and is a regular (non-dir) file.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// DirExists reports whether path exists and is a directory.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// SortStrings sorts s in place (ascending). A small insertion-free bubble sort,
// kept identical to the original package-main helper.
func SortStrings(s []string) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// FirstNonEmpty returns the first non-empty string in vals, or "".
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ScalarNode builds a string scalar YAML node.
func ScalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// FindMappingValue returns the value node for key in a YAML mapping node, or nil.
// (Like MapValue, but requires the key node to be a scalar — the form the
// migration transforms + the core loader's legacy-shape detection both use.)
func FindMappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(m.Content)-1; i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// MigrateCandidateYAMLFiles is the ONE candidate-file scanner the multi-document
// doc-migration steps share AND the core loader's legacy-vocab rejection scan uses:
// every `.yml`/`.yaml` under each of treeSubdirs (walked recursively, skipping
// nested git submodules + any `testdata` dir) plus the root-level YAML siblings in
// dir. Sorted, deduplicated.
func MigrateCandidateYAMLFiles(dir string, treeSubdirs []string) []string {
	seen := map[string]struct{}{}
	addYAMLTree := func(root string) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if filepath.Base(p) == "testdata" || IsGitSubmoduleDir(p, root) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml") {
				seen[filepath.Clean(p)] = struct{}{}
			}
			return nil
		})
	}
	for _, sub := range treeSubdirs {
		addYAMLTree(filepath.Join(dir, sub))
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
				seen[filepath.Clean(filepath.Join(dir, e.Name()))] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	SortStrings(out)
	return out
}

// OpUnifyCandidateFiles is the candidate-file set the op/plan-unify migrators AND
// the core loader's legacy-test-vocab rejection scan walk (candy/ + box/ trees +
// root siblings).
func OpUnifyCandidateFiles(dir string) []string {
	return MigrateCandidateYAMLFiles(dir, []string{"candy", "box"})
}

// MapValue returns the value node for key in a YAML mapping node, or nil.
func MapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// kindWordSet is the reserved node-kind-word set NodeShapedValue uses to detect a name-first
// node-form value: the core #Node kind words (spec.KindWords) UNIONED with the DEPLOYABLE resource
// kinds (spec.ResourceKinds) AND `candy`. ResourceKinds is unioned in so a substrate/group node
// value (`{vm: …}` / `{group: …}`) is still recognized after C2-group / C2-substrate dropped those
// kinds from KindWords; `candy` is added explicitly because C2-candy externalized the box⊻layer
// factory to a plugin (candy is in NEITHER KindWords — now EMPTY — NOR ResourceKinds, since it
// nests no deploy members), yet a `{candy: …}` value must still be recognized as node-shaped —
// without it, `charly migrate` re-migrates an already-node-form entity named after `candy` and
// classifyDoc mis-reads a node-form candy doc (idempotency + classification regression).
var kindWordSet = func() map[string]bool {
	m := make(map[string]bool, len(spec.KindWords)+len(spec.ResourceKinds)+1)
	for _, k := range spec.KindWords {
		m[k] = true
	}
	for _, k := range spec.ResourceKinds {
		m[k] = true
	}
	m["candy"] = true
	return m
}()

// NodeShapedValue reports whether a mapping node carries a reserved kind word as a
// key (i.e. it is a name-first node-form value).
func NodeShapedValue(val *yaml.Node) bool {
	if val == nil || val.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(val.Content); i += 2 {
		if kindWordSet[val.Content[i].Value] {
			return true
		}
	}
	return false
}

// FirstYAMLVersionLine extracts the value of the first top-level `version:` line.
func FirstYAMLVersionLine(data []byte) string {
	for line := range strings.SplitSeq(string(data), "\n") {
		if after, ok := strings.CutPrefix(line, "version:"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// IsGitSubmoduleDir reports whether p (≠ root) contains a .git entry (a nested
// submodule/repo boundary).
func IsGitSubmoduleDir(p, root string) bool {
	if p == root {
		return false
	}
	_, err := os.Stat(filepath.Join(p, ".git"))
	return err == nil
}

// NOTE: EnvdDir lives in kit (profile.go) — core aliases that copy (kit_aliases.go).
