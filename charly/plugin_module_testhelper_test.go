package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/refs"
)

// pluginModuleDir resolves a plugin candy's Go module dir from its STANDALONE repo (the candy
// de-submodule cutover, Phase 4): the in-repo candy/<name> dirs are deleted, so tests that
// host-build a plugin module must fetch the standalone repo (github.com/opencharly/plugin-<name>)
// at its newest tag and use the fetched candy/<name>/ dir as the build source.
func pluginModuleDir(t *testing.T, name string) string {
	t.Helper()
	tag := newestPluginTag(t, name)
	dir, err := refs.DownloadRepo("github.com/opencharly/plugin-"+name, tag)
	if err != nil {
		t.Fatalf("fetch plugin-%s@%s: %v", name, tag, err)
	}
	mod := filepath.Join(dir, "candy", "plugin-"+name)
	if _, err := os.Stat(filepath.Join(mod, "go.mod")); err != nil {
		t.Fatalf("plugin-%s module not at %s: %v", name, mod, err)
	}
	return mod
}

// newestPluginTag resolves a plugin repo's newest v-calver tag.
func newestPluginTag(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("git", "ls-remote", "--tags", refs.RepoGitURL("github.com/opencharly/plugin-"+name)).Output()
	if err != nil {
		t.Fatalf("git ls-remote plugin-%s: %v", name, err)
	}
	var tags []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		refName := strings.TrimPrefix(fields[1], "refs/tags/")
		if !strings.HasPrefix(refName, "v") || strings.HasSuffix(refName, "^{}") {
			continue
		}
		tags = append(tags, refName)
	}
	if len(tags) == 0 {
		t.Fatalf("no v-tags for plugin-%s", name)
	}
	return tags[len(tags)-1]
}
