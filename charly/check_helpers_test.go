package main

import (
	"net/http"
	"time"

	"github.com/opencharly/spec/spec"
)

// hostVerbResolverFor builds a *hostVerbResolver over a directly-constructed hostCheckCarrier
// (the SAME struct-literal shape production fills from a wire snapshot in
// plugin_dispatch_reverse.go — no kit.Runner involved) with the given venue executor + mode (+
// optional distro tags) — the in-proc host CheckContext / verb-dispatch source a compiled-in
// kit verb's RunVerb and the host provision/plugin helpers consume. dialTimeout/httpBase mirror
// kit.NewRunner's own zero-value defaults (3s / 10s) so fixture behavior stays unchanged.
func hostVerbResolverFor(exec spec.DeployExecutor, mode spec.CheckRunMode) *hostVerbResolver {
	return &hostVerbResolver{cc: &hostCheckCarrier{
		execFn:      func() spec.DeployExecutor { return exec },
		mode:        mode,
		dialTimeout: 3 * time.Second,
		httpBase:    &http.Client{Timeout: 10 * time.Second},
	}}
}

// hostVerbResolverWithCandyDirs builds a *hostVerbResolver over a hostCheckCarrier carrying the
// given committed-APK anchoring state — for exercising resolveCheckApk directly.
func hostVerbResolverWithCandyDirs(dirs map[string]string, scanErr error) *hostVerbResolver {
	return &hostVerbResolver{cc: &hostCheckCarrier{
		dialTimeout:  3 * time.Second,
		httpBase:     &http.Client{Timeout: 10 * time.Second},
		candyDirs:    dirs,
		candyScanErr: scanErr,
	}}
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
func testDistroConfig() *spec.DistroConfig {
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
func testBuilderCfg() *spec.BuilderConfig {
	_, builderCfg, _, err := LoadBuildConfigForBox(testdataDir)
	if err != nil {
		panic("failed to load builder config from testdata: " + err.Error())
	}
	return builderCfg
}
