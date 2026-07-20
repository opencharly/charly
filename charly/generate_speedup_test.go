package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// TestWriteContextIgnore verifies the generated .containerignore /
// .dockerignore carry the always-on baseline AND defaults.context_ignore, that
// duplicates are collapsed, and that both engine files are byte-identical in
// body (Item 1 of the build-speedup cutover).
func TestWriteContextIgnore(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{
		Dir: dir,
		Config: &Config{
			// "image" duplicated to exercise dedup against author input.
			Defaults: spec.BoxConfig{ContextIgnore: []string{"image", ".check", "image"}},
		},
	}
	if err := g.writeContextIgnore(); err != nil {
		t.Fatalf("writeContextIgnore: %v", err)
	}

	bodies := make([]string, 0, len(contextIgnoreFiles))
	for _, name := range contextIgnoreFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		s := string(data)
		// Baseline entries present (from baselineContextIgnore).
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

// TestRenderDnfConfWrite covers the dnf.conf bootstrap fragment (Item 4).
func TestRenderDnfConfWrite(t *testing.T) {
	if got := renderDnfConfWrite(nil); got != "" {
		t.Errorf("nil Dnf should render empty, got %q", got)
	}
	if got := renderDnfConfWrite(&vmshared.DnfConfig{}); got != "" {
		t.Errorf("zero Dnf should render empty, got %q", got)
	}
	got := renderDnfConfWrite(&vmshared.DnfConfig{MaxParallelDownloads: 10, Fastestmirror: true})
	for _, want := range []string{"max_parallel_downloads=10", "fastestmirror=True", ">> /etc/dnf/dnf.conf", "&& \\"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered dnf.conf fragment missing %q, got: %q", want, got)
		}
	}
	// Only one knob set → only that line.
	onlyParallel := renderDnfConfWrite(&vmshared.DnfConfig{MaxParallelDownloads: 5})
	if strings.Contains(onlyParallel, "fastestmirror") {
		t.Errorf("fastestmirror should be absent when unset, got %q", onlyParallel)
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
