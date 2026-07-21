package main

import (
	"os"
	"path/filepath"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// hostVerbResolverFor builds a *hostVerbResolver over a kit.Runner with the given venue
// executor + mode (+ optional distro tags) — the in-proc host CheckContext / verb-dispatch
// source a compiled-in kit verb's RunVerb and the host provision/plugin helpers consume. In
// production newCheckRunner builds one internally; a unit test dispatching a single verb (or a
// host helper directly) wants the resolver.
func hostVerbResolverFor(exec deploykit.DeployExecutor, mode RunMode, distros ...string) *hostVerbResolver {
	return newHostVerbResolver(kit.NewRunner(kit.RunnerConfig{Exec: exec, Mode: mode, Distros: distros}))
}

// hostVerbResolverWithCandyDirs builds a *hostVerbResolver over a kit.Runner carrying the given
// committed-APK anchoring state — for exercising resolveCheckApk directly.
func hostVerbResolverWithCandyDirs(dirs map[string]string, scanErr error) *hostVerbResolver {
	return newHostVerbResolver(kit.NewRunner(kit.RunnerConfig{CandyDirs: dirs, CandyScanErr: scanErr}))
}

// testdataDir is the project directory used by test fixtures. Tests read
// build config via LoadBuildConfigForBox(testdataDir) which goes through
// the unified loader (charly.yml + includes).
const testdataDir = "testdata"

// cmdOp builds the extracted `command` plugin-verb Op for tests. `command` left #OpVerb
// in the command→plugin extraction, so a command check/run is now `plugin: command` +
// plugin_input.command (the exec string), with the matchers exit_status/stdout/stderr
// staying on the step Op. The returned Op is plain — callers set any extra fields
// (RunAs/Context/ID/Stdout/Cache/Env) on it directly.
func cmdOp(command string) spec.Op {
	return spec.Op{Plugin: "command", PluginInput: map[string]any{"command": command}}
}

// cmdOpP is the *Op form of cmdOp, for call sites that need an addressable Op
// (e.g. &Op{Command: ...} became cmdOpP(...) in the command→plugin extraction).
func cmdOpP(command string) *spec.Op {
	o := cmdOp(command)
	return &o
}

// testDistroConfig returns the default DistroConfig from testdata fixtures for tests.
func testDistroConfig() *buildkit.DistroConfig {
	distroCfg, _, _, err := LoadBuildConfigForBox(testdataDir)
	if err != nil {
		panic("failed to load distro config from testdata: " + err.Error())
	}
	return distroCfg
}

// testDistroDef returns the resolved DistroDef for the given distro tags.
func testDistroDef(tags ...string) *spec.ResolvedDistro {
	dc := testDistroConfig()
	return dc.ResolveDistro(tags)
}

// testBuilderCfg returns the default BuilderConfig from testdata fixtures for tests.
func testBuilderCfg() *buildkit.BuilderConfig {
	_, builderCfg, _, err := LoadBuildConfigForBox(testdataDir)
	if err != nil {
		panic("failed to load builder config from testdata: " + err.Error())
	}
	return builderCfg
}

// testProjectDir writes a minimal valid charly.yml (+ build.yml) to a
// tmpdir and returns its path. Use when a test needs a real project dir
// argument for Validate / ResolveBox calls that no longer tolerate dir="".
// The emitted project has fedora + arch + debian + ubuntu distros and
// a pixi builder — enough to cover most fixture Configs without error.
func testProjectDir(t interface {
	TempDir() string
	Fatalf(string, ...any)
	Helper()
}) string {
	t.Helper()
	tmpdir := t.TempDir()
	// Reuse testdata's build.yml (and testdata itself as the helper's dir when
	// the caller didn't need tmpdir specifically) — it's a complete fixture.
	root := []byte("version: 2026.202.0105\nimport: [build.yml]\n")
	if err := os.WriteFile(filepath.Join(tmpdir, "charly.yml"), root, 0644); err != nil {
		t.Fatalf("writing charly.yml: %v", err)
	}
	src, err := os.ReadFile("testdata/build.yml")
	if err != nil {
		t.Fatalf("reading testdata/build.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpdir, "build.yml"), src, 0644); err != nil {
		t.Fatalf("writing build.yml: %v", err)
	}
	return tmpdir
}
