package marketplace

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// prune.go — the generated-artifact boundary. `scanGenerated` returns every canonical path that
// is CURRENTLY generated on disk: header-carrying markdown under the owned corpus trees
// (<out>/<family>/skills + agents), the known JSON paths (per-family plugin.json/.mcp.json +
// the marketplace root manifests + the setup launcher), and .claude/hooks under root (fully
// generated — every gate script + aux file is a hook entity). The boundary is content (the
// header) for markdown and known-path + existence for JSON/hooks — never location inference.
// Hand-authored files (README.md, CHANGELOG/, LICENSE, scripts/, kimi-user-config.toml) carry no
// header and sit outside the known paths, so they are preserved. `generate` deletes the scanned
// set before writing; `drift` flags a scanned path absent from the emissions map as stale.

func pruneGenerated(root, out string, families []family, ks *kindSet) error {
	paths, err := scanGenerated(root, out, families, ks)
	if err != nil {
		return err
	}
	for _, rel := range paths {
		base, target := root, rel
		if strings.HasPrefix(rel, corpusPrefix) {
			base, target = out, strings.TrimPrefix(rel, corpusPrefix)
		}
		if err := os.Remove(filepath.Join(base, filepath.FromSlash(target))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	// Sweep empty dirs left under the owned trees.
	for _, f := range families {
		for _, sub := range []string{"skills", "agents"} {
			_ = removeEmptyDirs(filepath.Join(out, f.Name, sub))
		}
	}
	return nil
}

// scanGenerated lists the canonical paths of every currently-generated artifact that EXISTS,
// under both trees: the corpus under outDir and the harness surface under root.
func scanGenerated(root, outDir string, families []family, ks *kindSet) ([]string, error) {
	var out []string
	addCorpus := func(rel string) {
		if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(strings.TrimPrefix(rel, corpusPrefix)))); err == nil {
			out = append(out, rel)
		}
	}
	// 1. header-scanned markdown trees: <out>/<family>/skills + agents.
	for _, f := range families {
		for _, sub := range []string{"skills", "agents"} {
			dir := filepath.Join(outDir, f.Name, sub)
			if _, err := os.Stat(dir); err != nil {
				continue
			}
			err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if hasGeneratedHeader(b) {
					out = append(out, corpusPrefix+fileKey(outDir, path))
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	// 2. known JSON paths per family + the marketplace root + the setup launcher.
	for _, f := range families {
		addCorpus("plugins/" + f.Name + "/.claude-plugin/plugin.json")
		addCorpus("plugins/" + f.Name + "/.codex-plugin/plugin.json")
		addCorpus("plugins/" + f.Name + "/.mcp.json")
	}
	addCorpus("plugins/.claude-plugin/marketplace.json")
	addCorpus("plugins/.agents/plugins/marketplace.json")
	addCorpus("plugins/kimi.plugin.json")
	addCorpus("plugins/package.json")
	addCorpus("plugins/profiles.json")
	addCorpus("plugins/setup")
	// 3. .claude/hooks under root — entirely generated (every gate + aux file is a hook entity).
	hooksDir := filepath.Join(root, ".claude", "hooks")
	if ents, err := os.ReadDir(hooksDir); err == nil {
		for _, de := range ents {
			if de.IsDir() {
				continue
			}
			out = append(out, fileKey(root, filepath.Join(hooksDir, de.Name())))
		}
	}
	return out, nil
}

// removeEmptyDirs deletes empty directories under dir, bottom-up (no-op when dir absent).
func removeEmptyDirs(dir string) error {
	if _, err := os.Stat(dir); err != nil {
		return nil
	}
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != dir {
			_ = os.Remove(path) // only succeeds when empty; ignore failures
		}
		return nil
	})
}

func bytesEqual(a, b []byte) bool { return bytes.Equal(a, b) }
