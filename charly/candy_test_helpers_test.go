package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/spec"
)

// candy_test_helpers_test.go — the shared spec.CandyReader test-fixture constructor (W9,
// decoupled off sdk/deploykit in the #55 final-tail closure). Every test in this package that
// used to build a candy fixture as a literal *Candy{...} now builds a (spec.CandyModel,
// spec.CandyView) pair instead — the SAME split the real scan pipeline produces (CandyModel =
// the build-plan/package/env half, CandyView = the identity/graph half) — and wraps it via
// testCandy.
//
// testCandy used to delegate to sdk/deploykit.NewSpecCandyModel (the specCandyAdapter). That
// adapter's interface (deploykit.CandyModel) is a plain alias of spec.CandyReader, and every
// field/method it reads is ALREADY spec-native (CandyModel/CandyView are CUE-generated to
// match the interface exactly) — so this file ports the adapter body directly, verbatim
// field-for-field, over spec types only (no sdk import). One copy per module is the
// established pattern (each plugin candy module already carries its own testCandy —
// candy/plugin-loader, candy/plugin-fleet — R3 is satisfied per-module, not globally, since
// Go modules cannot share _test.go files).
type testCandyReader struct {
	m  spec.CandyModel
	v  spec.CandyView
	sd string
}

// testCandy wraps a CandyModel + CandyView into a spec.CandyReader fixture, stamping name onto
// BOTH views (GetName()/GetSourceDir() read CandyModel; identity/graph fields like Remote/RepoPath
// read CandyView, so both need the same Name for a fixture that behaves consistently regardless of
// which accessor a test path happens to call).
func testCandy(name string, m spec.CandyModel, v spec.CandyView) spec.CandyReader {
	m.Name = name
	v.Name = name
	return &testCandyReader{m: m, v: v, sd: m.SourceDir}
}

func (a *testCandyReader) GetName() string      { return a.m.Name }
func (a *testCandyReader) GetSourceDir() string { return a.sd }
func (a *testCandyReader) GetVersion() string   { return a.m.Version }
func (a *testCandyReader) Reboot() bool         { return a.m.Reboot }
func (a *testCandyReader) GetRemote() bool      { return a.v.Remote }
func (a *testCandyReader) GetRepoPath() string  { return a.v.RepoPath }

func (a *testCandyReader) Vars() map[string]string      { return a.m.Vars }
func (a *testCandyReader) PlanSteps() []spec.Step       { return a.m.Plan }
func (a *testCandyReader) Apk() []spec.ApkPackageSpec   { return a.m.Apk }
func (a *testCandyReader) TopPackages() []string        { return a.m.TopPackages }
func (a *testCandyReader) ServiceFiles() []string       { return a.m.ServiceFiles }
func (a *testCandyReader) RelayPorts() []int            { return a.m.PortRelayPorts }
func (a *testCandyReader) RunOps() []spec.Op            { return a.m.RunOps }
func (a *testCandyReader) HasTasks() bool               { return len(a.m.RunOps) > 0 }
func (a *testCandyReader) HasExtract() bool             { return len(a.m.Extract) > 0 }
func (a *testCandyReader) Extract() []spec.CandyExtract { return a.m.Extract }
func (a *testCandyReader) HasData() bool                { return len(a.m.Data) > 0 }
func (a *testCandyReader) Data() []spec.CandyData       { return a.m.Data }

//nolint:staticcheck // LocalPkg is deprecated (replaced by Packaging) but spec.CandyReader still requires it
func (a *testCandyReader) LocalPkg(format string) string { return a.m.LocalPkg[format] }
func (a *testCandyReader) Packaging() *spec.Packaging    { return a.m.Packaging }
func (a *testCandyReader) FormatSection(name string) *spec.PackageSection {
	if s, ok := a.m.FormatSections[name]; ok {
		return &s
	}
	return nil
}
func (a *testCandyReader) TagSection(tag string) *spec.TagPkgConfig {
	if s, ok := a.m.TagSections[tag]; ok {
		return &s
	}
	return nil
}
func (a *testCandyReader) HasEnv() bool                        { return a.m.Env != nil }
func (a *testCandyReader) EnvConfig() (*spec.EnvConfig, error) { return a.m.Env, nil }
func (a *testCandyReader) HasRoute() bool                      { return a.m.Route != nil }
func (a *testCandyReader) Route() (*spec.RouteConfig, error)   { return a.m.Route, nil }
func (a *testCandyReader) Shell() *spec.Shell                  { return a.m.Shell }
func (a *testCandyReader) Service() []spec.CandyService        { return a.m.Service }

func (a *testCandyReader) GetIncludedCandy() []spec.CandyRefEntry {
	return testWrapCandyRefs(a.v.IncludedCandy)
}
func (a *testCandyReader) GetRequire() []spec.CandyRefEntry { return testWrapCandyRefs(a.v.Require) }
func (a *testCandyReader) GetBakePlugin() []spec.CandyRefEntry {
	return testWrapCandyRefs(a.m.BakePlugin)
}

// testWrapCandyRefs wraps the bare-string wire form (spec.CandyRef = string) into the runtime
// spec.CandyRefEntry model — the same wrapping specCandyAdapter's specRefsToDk did.
func testWrapCandyRefs(in []spec.CandyRef) []spec.CandyRefEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]spec.CandyRefEntry, len(in))
	for i, r := range in {
		out[i] = spec.CandyRefEntry{Raw: r}
	}
	return out
}

func (a *testCandyReader) HasFile(filename string) bool { return a.fileExists(filename) }
func (a *testCandyReader) GetHasPackageJson() bool      { return a.fileExists("package.json") }
func (a *testCandyReader) GetHasCargoToml() bool        { return a.fileExists("Cargo.toml") }
func (a *testCandyReader) GetHasPixiLock() bool         { return a.fileExists("pixi.lock") }
func (a *testCandyReader) PixiManifest() string {
	for _, f := range []string{"pixi.toml", "pyproject.toml", "environment.yml"} {
		if a.fileExists(f) {
			return f
		}
	}
	return ""
}
func (a *testCandyReader) fileExists(name string) bool {
	if a.sd == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(a.sd, name))
	return err == nil
}

func (a *testCandyReader) HasFormatPackages() bool {
	return len(a.m.FormatSections) > 0 || len(a.m.TagSections) > 0 || len(a.m.TopPackages) > 0
}
func (a *testCandyReader) HasContent() bool             { return a.m.HasContent }
func (a *testCandyReader) HasInstallFiles() bool        { return a.m.HasInstallFiles }
func (a *testCandyReader) HasInit(initName string) bool { return a.v.InitSystems[initName] }
func (a *testCandyReader) GetExternalBuilder() string   { return a.m.ExternalBuilder }
func (a *testCandyReader) GetStatus() string            { return a.v.Status }
func (a *testCandyReader) GetDescription() string       { return a.v.Description }
func (a *testCandyReader) GetSubPathPrefix() string     { return a.v.SubPathPrefix }

func (a *testCandyReader) Security() *spec.Security            { return a.m.Security }
func (a *testCandyReader) Hooks() *spec.CandyHook              { return a.m.Hook }
func (a *testCandyReader) EnvRequire() []spec.EnvDependency    { return a.m.EnvRequire }
func (a *testCandyReader) EnvAccept() []spec.EnvDependency     { return a.m.EnvAccept }
func (a *testCandyReader) SecretRequire() []spec.EnvDependency { return a.m.SecretRequire }
func (a *testCandyReader) SecretAccept() []spec.EnvDependency  { return a.m.SecretAccept }
func (a *testCandyReader) MCPRequire() []spec.EnvDependency    { return a.m.MCPRequire }
func (a *testCandyReader) MCPAccept() []spec.EnvDependency     { return a.m.MCPAccept }

func (a *testCandyReader) Alias() []spec.CandyAlias            { return a.v.Aliases }
func (a *testCandyReader) HasAliases() bool                    { return len(a.v.Aliases) > 0 }
func (a *testCandyReader) Volume() []spec.CandyVolume          { return a.v.Volumes }
func (a *testCandyReader) HasVolumes() bool                    { return len(a.v.Volumes) > 0 }
func (a *testCandyReader) EnvProvides() map[string]string      { return a.v.EnvProvides }
func (a *testCandyReader) MCPProvide() []spec.CandyMCPProvide  { return a.v.MCPProvide }
func (a *testCandyReader) Artifact() []spec.CandyArtifact      { return a.m.Artifact }
func (a *testCandyReader) Capabilities() *spec.CandyCapability { return a.m.Capability }
func (a *testCandyReader) RequiresCapabilities() []string      { return a.m.RequiresCapability }
func (a *testCandyReader) Engine() string                      { return a.m.Engine }
func (a *testCandyReader) Secret() []spec.CandySecret          { return a.m.Secret }
func (a *testCandyReader) PortSpecs() []spec.PortSpec          { return a.m.Port }
func (a *testCandyReader) HasPorts() bool                      { return len(a.m.Port) > 0 }
func (a *testCandyReader) HasEnvAccepts() bool                 { return len(a.m.EnvAccept) > 0 }
func (a *testCandyReader) HasEnvProvides() bool                { return len(a.v.EnvProvides) > 0 }
func (a *testCandyReader) HasEnvRequires() bool                { return len(a.m.EnvRequire) > 0 }
func (a *testCandyReader) HasMCPAccepts() bool                 { return len(a.m.MCPAccept) > 0 }
func (a *testCandyReader) HasMCPProvides() bool                { return len(a.v.MCPProvide) > 0 }
func (a *testCandyReader) HasMCPRequires() bool                { return len(a.m.MCPRequire) > 0 }
func (a *testCandyReader) HasSecretAccepts() bool              { return len(a.m.SecretAccept) > 0 }
func (a *testCandyReader) HasSecretRequires() bool             { return len(a.m.SecretRequire) > 0 }

func (a *testCandyReader) IsPluginCandy() bool          { return a.v.IsPlugin }
func (a *testCandyReader) GetPluginSource() string      { return a.v.PluginSource }
func (a *testCandyReader) GetPluginProviders() []string { return a.v.PluginProviders }

func (a *testCandyReader) AgentProvide() []spec.AgentRuntimeCapability { return a.v.AgentProvide }
func (a *testCandyReader) HasAgentProvides() bool                      { return len(a.v.AgentProvide) > 0 }
func (a *testCandyReader) TerminalProfiles() map[string]spec.TerminalProfile {
	return a.v.TerminalProfiles
}

//nolint:staticcheck // LocalPkg is deprecated (replaced by Packaging) but spec.CandyReader still requires it
func (a *testCandyReader) LocalPkgFormats() []string {
	if len(a.m.LocalPkg) == 0 {
		return nil
	}
	out := make([]string, 0, len(a.m.LocalPkg))
	for f := range a.m.LocalPkg {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func (a *testCandyReader) Port() ([]string, error) {
	if len(a.m.Port) == 0 {
		return nil, nil
	}
	out := make([]string, len(a.m.Port))
	for i, p := range a.m.Port {
		if p.Protocol == "udp" {
			out[i] = strconv.Itoa(p.Port) + "/udp"
		} else {
			out[i] = strconv.Itoa(p.Port)
		}
	}
	return out, nil
}

var _ spec.CandyReader = (*testCandyReader)(nil)

// testConstructStepExecutor returns the SAME in-proc reverse-channel executor
// the invokeOpCompile helper (fleet_compile_parity_test.go) threads onto the ctx it hands
// command:fleet's OpCompile (K5-A item 1, compile-seam ctx-threading): a test
// exercising charly's own construct-step routing directly (in-process, package
// main) needs a REAL executor reaching the provider registry for any `run: plugin:
// <word>` op, exactly as the real compile path does — the "construct-step" HostBuild
// seam has no other way to be reached from a plain function call.
func testConstructStepExecutor() (context.Context, *specexec.Executor) {
	ex := specexec.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}})
	return specexec.ContextWithExecutor(context.Background(), ex), ex
}

// testCompileOpSteps drives the REAL "construct-step" host builder (hostBuildConstructStep,
// host_build_construct_step.go) over the layer's build/deploy-context run: steps — the SAME
// registry-consult decision sdk/deploykit's CompileOpSteps reaches via the "construct-step"
// HostBuild seam, called here DIRECTLY (in-proc, no wire round-trip) since this test binary
// IS the host. This is intentionally NOT a port of sdk/deploykit's CompileOpSteps/
// constructOpStep/buildGenericOpStep machinery (that would be R3 duplication of the compiler
// engine, already covered by sdk/deploykit/plan_compile_test.go's stub-driven mechanics
// coverage) — it exercises exactly the one genuinely charly-side decision these tests assert:
// which REAL registered provider a `run: plugin: <word>` op resolves to, and whether that
// provider's TypedStepProvider lowers it to a typed step (SystemPackagesStep/
// ServicePackagedStep) instead of a generic OpStep. The context-filter (build/deploy vs
// runtime-only) mirrors CompileOpSteps' own filter using the SAME spec.OpInContext DI hook
// production wires (charly/layers.go's init()); the non-plugin / empty-reply generic-OpStep
// fallback is a MINIMAL literal construction (only the fields these tests assert on), not the
// full buildGenericOpStep (whose ResolveUserSpec-driven RunAs resolution is already unit-tested
// directly in sdk/deploykit/tasks_emit_test.go::TestResolveUserSpec — re-deriving it here would
// be redundant coverage, not a loss).
func testCompileOpSteps(t *testing.T, layer spec.CandyReader) []spec.InstallStep {
	t.Helper()
	var out []spec.InstallStep
	steps := layer.PlanSteps()
	for i := range steps {
		step := &steps[i]
		kw, err := step.StepKind()
		if err != nil || kw != spec.KwRun {
			continue
		}
		op := &step.Op
		if spec.OpInContext(op, spec.CtxRuntime) && !spec.OpInContext(op, spec.CtxBuild) && !spec.OpInContext(op, spec.CtxDeploy) {
			continue
		}
		s, err := testConstructOpStep(t, op, layer)
		if err != nil {
			t.Fatalf("construct-step: %v", err)
		}
		if s != nil {
			out = append(out, s)
		}
	}
	return out
}

// testConstructOpStep mirrors deploykit.constructOpStep's dispatch: a non-`plugin:` verb (or a
// `plugin:` verb the real construct-step handler declines) lowers to a minimal literal OpStep;
// a `plugin:` verb the handler recognizes as a TypedStepProvider decodes to the real typed step
// via the SAME spec.StepFromView the wire seam uses.
func testConstructOpStep(t *testing.T, op *spec.Op, layer spec.CandyReader) (spec.InstallStep, error) {
	t.Helper()
	verb, err := op.Kind()
	if err != nil {
		return nil, nil
	}
	if verb == "plugin" {
		req := spec.ConstructStepRequest{
			Op:           *op,
			CandyName:    layer.GetName(),
			ResolvedUser: testResolveRunAsUser(op.RunAs),
			PkgFormat:    "rpm",
			DistroTags:   []string{"all", "rpm"},
		}
		reply, err := hostBuildConstructStep(context.Background(), req, buildEngineContext{})
		if err != nil {
			return nil, err
		}
		if reply.Step != nil {
			return spec.StepFromView(*reply.Step)
		}
	}
	return &spec.OpStep{
		Op:           op,
		CandyName:    layer.GetName(),
		CandyDir:     layer.GetSourceDir(),
		CtxPath:      layer.GetSourceDir(),
		ResolvedUser: testResolveRunAsUser(op.RunAs),
	}, nil
}

// testResolveRunAsUser is a minimal test-local stand-in for the real ResolveUserSpec
// (sdk/deploykit/tasks_emit.go, already directly unit-tested in
// sdk/deploykit/tasks_emit_test.go::TestResolveUserSpec) — it covers only the root/0/empty
// scope-collapse case these fixtures exercise, never the full ${USER}/numeric/colon
// substitution the real function handles.
func testResolveRunAsUser(runAs string) string {
	u := strings.TrimSpace(runAs)
	if u == "" || u == "root" || u == "0" {
		return "0"
	}
	return u
}

// testCompileServiceSteps (the deploykit.CompileServiceSteps-driving twin of testCompileOpSteps
// above) relocated to candy/plugin-fleet (#55 decoupling, Batch A) with its own copy — its
// last charly-side consumer, service_distro_filter_test.go, moved there too.
