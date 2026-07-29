package build

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// host_prep_test.go — migrated from charly/generate_speedup_test.go (K3 host-prep move,
// coneB-render): writeContextIgnore moved to host_prep.go (a pure function of dir/cfg/baseline,
// no host-only dependency). testBaseline mirrors charly/charly.yml's context_ignore_baseline:
// directive verbatim (a separate module can't //go:embed the charly binary's embedded config).
var testBaseline = []string{
	".git", "bin", "charly", "*.md",
	"**/__pycache__", "**/*.pyc", "**/*.pyo", "**/*.egg-info",
	"**/node_modules", "**/.git", "**/.DS_Store", "**/*~", "**/*.swp", "**/*.swo",
	"**/.pytest_cache", "**/.mypy_cache",
}

// TestWriteContextIgnore verifies the generated .containerignore / .dockerignore carry the
// always-on baseline AND defaults.context_ignore, that duplicates are collapsed, and that both
// engine files are byte-identical in body.
func TestWriteContextIgnore(t *testing.T) {
	dir := t.TempDir()
	cfg := &spec.Config{
		// "image" duplicated to exercise dedup against author input.
		Defaults: spec.BoxConfig{ContextIgnore: []string{"image", ".check", "image"}},
	}
	if err := writeContextIgnore(dir, cfg, testBaseline); err != nil {
		t.Fatalf("writeContextIgnore: %v", err)
	}

	bodies := make([]string, 0, len(contextIgnoreFiles))
	for _, name := range contextIgnoreFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		s := string(data)
		// Baseline entries present.
		for _, want := range []string{".git", "bin", "charly", "*.md", "**/__pycache__", "**/node_modules"} {
			if !ciLineContains(s, want) {
				t.Errorf("%s missing baseline entry %q", name, want)
			}
		}
		// Config additions present.
		for _, want := range []string{"image", ".check"} {
			if !ciLineContains(s, want) {
				t.Errorf("%s missing config entry %q", name, want)
			}
		}
		// Dedup: "image" appears exactly once as a whole line.
		if n := ciCountLine(s, "image"); n != 1 {
			t.Errorf("%s: 'image' appears %d times, want 1 (dedup)", name, n)
		}
		// Generated header present.
		if !strings.HasPrefix(s, "# "+name+" (generated") {
			t.Errorf("%s missing generated header, got first line %q", name, ciFirstLine(s))
		}
		bodies = append(bodies, ciStripFirstLine(s))
	}
	if len(bodies) == 2 && bodies[0] != bodies[1] {
		t.Errorf(".containerignore and .dockerignore bodies differ:\n%q\nvs\n%q", bodies[0], bodies[1])
	}
}

func ciFirstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before
	}
	return s
}

func ciStripFirstLine(s string) string {
	if _, after, ok := strings.Cut(s, "\n"); ok {
		return after
	}
	return ""
}

func ciLineContains(s, want string) bool {
	return slices.Contains(strings.Split(s, "\n"), want)
}

func ciCountLine(s, want string) int {
	n := 0
	for ln := range strings.SplitSeq(s, "\n") {
		if ln == want {
			n++
		}
	}
	return n
}
