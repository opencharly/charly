package main

import (
	"os"
	"path/filepath"
	"testing"
)

// plugin_build_stamp_test.go — the red/green pair for content-stamped plugin-build reuse.
//
// The property that matters is a conjunction, and both halves have teeth: an UNCHANGED source tree
// must stamp identically (so the ~30MB relink is skipped — the relink storm this fixes), and ANY
// change to the candy OR to a module it reaches through a local `replace` must change the stamp (so
// the #76 staleness rule survives a submodule bump).

// newStampFixture writes a candy module that replaces a local sdk module, which in turn replaces a
// local spec module — the real shape (candy → sdk → spec) the transitive root walk must follow.
func newStampFixture(t *testing.T) (candyDir, sdkDir, specDir string) {
	t.Helper()
	root := t.TempDir()
	candyDir = filepath.Join(root, "candy", "plugin-example")
	sdkDir = filepath.Join(root, "sdk")
	specDir = filepath.Join(root, "spec")
	for _, d := range []string{candyDir, sdkDir, specDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(candyDir, "go.mod", "module example.com/candy\n\ngo 1.26.0\n\nrequire example.com/sdk v0.0.0\n\nreplace example.com/sdk => ../../sdk\n")
	write(candyDir, "main.go", "package main\n\nfunc main() {}\n")
	write(sdkDir, "go.mod", "module example.com/sdk\n\ngo 1.26.0\n\nreplace (\n\texample.com/spec => ../spec\n)\n")
	write(sdkDir, "sdk.go", "package sdk\n")
	write(specDir, "go.mod", "module example.com/spec\n\ngo 1.26.0\n")
	write(specDir, "spec.go", "package spec\n")
	return candyDir, sdkDir, specDir
}

func stampOf(t *testing.T, dir string) string {
	t.Helper()
	s, err := pluginBuildStamp(dir, "./cmd/serve", "-buildvcs=false", os.Environ())
	if err != nil {
		t.Fatalf("pluginBuildStamp() error = %v", err)
	}
	if s == "" {
		t.Fatal("pluginBuildStamp() returned an empty stamp")
	}
	return s
}

// TestPluginBuildStamp_UnchangedSourceIsStable is the GREEN arm: with nothing touched, the stamp
// repeats, so pluginBinaryIsFresh answers true and the relink is skipped.
func TestPluginBuildStamp_UnchangedSourceIsStable(t *testing.T) {
	candyDir, _, _ := newStampFixture(t)
	first := stampOf(t, candyDir)
	if second := stampOf(t, candyDir); second != first {
		t.Fatalf("stamp changed with no source edit (%s -> %s) — every charly subprocess would relink every plugin, the storm this fixes", first, second)
	}
	bin := filepath.Join(t.TempDir(), "plugin-example-abc123")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if pluginBinaryIsFresh(bin, first) {
		t.Error("pluginBinaryIsFresh() = true with no stamp recorded — an unstamped binary must never be reused")
	}
	writePluginStamp(bin, first)
	if !pluginBinaryIsFresh(bin, first) {
		t.Error("pluginBinaryIsFresh() = false right after writePluginStamp — the reuse path can never fire")
	}
}

// TestPluginBuildStamp_AnySourceChangeInvalidates is the RED arm: it walks the three trees a plugin
// build depends on and asserts each one, edited alone, changes the stamp. A stamp that missed any of
// these would hand back a stale binary — precisely the failure #76's always-rebuild guarded against.
func TestPluginBuildStamp_AnySourceChangeInvalidates(t *testing.T) {
	candyDir, sdkDir, specDir := newStampFixture(t)
	base := stampOf(t, candyDir)

	cases := []struct {
		what string
		do   func()
	}{
		{"the candy's own source", func() {
			mustWrite(t, filepath.Join(candyDir, "main.go"), "package main\n\nfunc main() { println(\"edited\") }\n")
		}},
		{"a new file in the candy", func() {
			mustWrite(t, filepath.Join(candyDir, "extra.go"), "package main\n")
		}},
		{"the replaced sdk module (a submodule bump, or an uncommitted edit inside it)", func() {
			mustWrite(t, filepath.Join(sdkDir, "sdk.go"), "package sdk\n\nvar Added = 1\n")
		}},
		{"the transitively-replaced spec module", func() {
			mustWrite(t, filepath.Join(specDir, "spec.go"), "package spec\n\nvar Added = 1\n")
		}},
		{"the candy's dependency pins (go.sum)", func() {
			mustWrite(t, filepath.Join(candyDir, "go.sum"), "example.com/dep v1.0.0 h1:deadbeef\n")
		}},
	}
	prev := base
	for _, tc := range cases {
		tc.do()
		got := stampOf(t, candyDir)
		if got == prev {
			t.Errorf("stamp unchanged after editing %s — a stale binary would be reused", tc.what)
		}
		prev = got
	}
}

// TestPluginBuildStamp_BuildInputsInvalidate covers the non-source inputs that also change the
// produced bytes: the build target, the vcs flag, and the build-relevant environment.
func TestPluginBuildStamp_BuildInputsInvalidate(t *testing.T) {
	candyDir, _, _ := newStampFixture(t)
	base, err := pluginBuildStamp(candyDir, "./cmd/serve", "-buildvcs=false", []string{"GOOS=linux", "GOARCH=amd64"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		what        string
		target, vcs string
		env         []string
	}{
		{"a different build target", ".", "-buildvcs=false", []string{"GOOS=linux", "GOARCH=amd64"}},
		{"a different buildvcs flag", "./cmd/serve", "-buildvcs=auto", []string{"GOOS=linux", "GOARCH=amd64"}},
		{"a different GOARCH", "./cmd/serve", "-buildvcs=false", []string{"GOOS=linux", "GOARCH=arm64"}},
		{"CGO toggled", "./cmd/serve", "-buildvcs=false", []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=1"}},
	} {
		got, err := pluginBuildStamp(candyDir, tc.target, tc.vcs, tc.env)
		if err != nil {
			t.Fatalf("%s: %v", tc.what, err)
		}
		if got == base {
			t.Errorf("stamp unchanged with %s — a binary built for different inputs would be reused", tc.what)
		}
	}
}

// TestLocalReplaceDirs_OnlyLocalPaths asserts the go.mod parse picks up both the single-line and
// block replace forms and ignores a module-version replace (already pinned by go.sum, which is
// hashed as part of the candy tree).
func TestLocalReplaceDirs_OnlyLocalPaths(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), `module example.com/x

go 1.26.0

replace example.com/a => ../a

replace (
	example.com/b => ./vendored/b
	example.com/c => example.com/c-fork v1.2.3
)
`)
	got := localReplaceDirs(dir)
	want := map[string]bool{
		filepath.Clean(filepath.Join(dir, "../a")):       true,
		filepath.Clean(filepath.Join(dir, "vendored/b")): true,
	}
	if len(got) != len(want) {
		t.Fatalf("localReplaceDirs() = %v, want exactly the two LOCAL-path replaces (%v)", got, want)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("localReplaceDirs() returned %q — a module-version replace is not a local path and must be ignored", g)
		}
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
