package marketplace

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
)

// prune.go — the generated-artifact boundary. `scanGenerated` returns every repo-relative path
// that is CURRENTLY generated on disk: header-carrying markdown under the owned trees
// (plugins/<family>/skills + agents), the known JSON paths (per-family plugin.json/.mcp.json +
// the marketplace root manifests + the setup launcher), and .claude/hooks (fully generated —
// every gate script + aux file is a hook entity). The boundary is content (the header) for
// markdown and known-path + existence for JSON/hooks — never location inference. Hand-authored
// files (plugins/README.md, plugins/CHANGELOG/, plugins/LICENSE, plugins/scripts/squash_body.py,
// plugins/kimi-user-config.toml) carry no header and sit outside the known paths, so they are
// preserved. `generate` deletes the scanned set before writing; `drift` flags a scanned path
// absent from the emissions map as stale.

func pruneGenerated(root string, families []family, ks *kindSet) error {
	paths, err := scanGenerated(root, families, ks)
	if err != nil {
		return err
	}
	for _, rel := range paths {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	// Sweep empty dirs left under the owned trees.
	for _, f := range families {
		for _, sub := range []string{"skills", "agents"} {
			_ = removeEmptyDirs(filepath.Join(root, "plugins", f.Name, sub))
		}
	}
	return nil
}

// scanGenerated lists the repo-relative paths of every currently-generated artifact that EXISTS.
func scanGenerated(root string, families []family, ks *kindSet) ([]string, error) {
	var out []string
	add := func(rel string) {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			out = append(out, rel)
		}
	}
	// 1. header-scanned markdown trees: plugins/<family>/skills + agents.
	pluginsDir := filepath.Join(root, "plugins")
	for _, f := range families {
		for _, sub := range []string{"skills", "agents"} {
			dir := filepath.Join(pluginsDir, f.Name, sub)
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
					out = append(out, fileKey(root, path))
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
		add("plugins/" + f.Name + "/.claude-plugin/plugin.json")
		add("plugins/" + f.Name + "/.codex-plugin/plugin.json")
		add("plugins/" + f.Name + "/.mcp.json")
	}
	add("plugins/.claude-plugin/marketplace.json")
	add("plugins/profiles.json")
	add("plugins/setup")
	// 3. .claude/hooks — entirely generated (every gate + aux file is a hook entity).
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
