package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/vmshared"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// Tests for the pod-overlay step-emit dispatch (charly/oci_step_emit.go's ociEmitStep — the
// single source of truth after the P11c overlay-walker relocation to sdk/deploykit). The former
// core overlay walker struct is GONE (the kind-blind walker now lives in sdk/deploykit/oci_target.go
// as deploykit.OCITarget); these tests exercise the REAL core dispatch through the SAME seam the
// candy uses in production: a deploykit.OCITarget whose EmitStepOp delegates to ociEmitStep. The
// walker's `# Layer:` headers + home resolution are preserved (mirrors the former in-core overlay
// walker Emit); the per-step fragment comes from ociEmitStep — byte-identical to the pre-move core
// render (the dispatch is UNCHANGED).

// ociTestTarget constructs a deploykit.OCITarget wired to the core ociEmitStep dispatch over the
// given host buildEngineContext, so the tests exercise the real dispatch through the production
// seam (deploykit.OCITarget.EmitStepOp → HostBuild("step-emit","oci-emit-step") → ociEmitStep).
// Home/Distros are empty (the tests that need home resolution or per-step distros are rare; add a
// dedicated constructor if one arises).
func ociTestTarget(build buildEngineContext) *deploykit.OCITarget {
	return &deploykit.OCITarget{
		EmitStepOp: func(step spec.InstallStep, plan *spec.InstallPlan, d []string) (string, error) {
			return ociEmitStep(step, plan, d, build)
		},
	}
}

func TestOCITargetEmitShellHook(t *testing.T) {
	tgt := ociTestTarget(buildEngineContext{})
	plan := &deploykit.InstallPlan{Candy: "uv", Steps: []spec.InstallStep{
		&deploykit.ShellHookStep{
			CandyName: "uv",
			EnvVars: map[string]string{
				"UV_INSTALL_DIR": "/usr/local/bin",
			},
			PathAdd: []string{"$HOME/.cargo/bin"},
		},
	}}
	if err := tgt.Emit([]*deploykit.InstallPlan{plan}, deploykit.EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, `ENV UV_INSTALL_DIR="/usr/local/bin"`) {
		t.Errorf("missing ENV var: %s", got)
	}
	if !strings.Contains(got, "ENV PATH=$HOME/.cargo/bin:$PATH") {
		t.Errorf("missing PATH prepend: %s", got)
	}
	if !strings.Contains(got, "# Layer: uv") {
		t.Errorf("missing layer header: %s", got)
	}
}

func TestOCITargetEmitSystemPackagesWithLegacyTemplate(t *testing.T) {
	// Legacy InstallTemplate set; PhaseTemplate returns it for (install, container).
	distro := &spec.ResolvedDistro{
		Format: map[string]*FormatDef{
			"rpm": {
				InstallTemplate: "RUN dnf install -y {{join .Packages \" \"}}\n",
			},
		},
	}
	tgt := ociTestTarget(buildEngineContext{DistroCfg: buildkit.WrapDistroDef(distro)})
	plan := &deploykit.InstallPlan{Candy: "ripgrep", Steps: []spec.InstallStep{
		&deploykit.SystemPackagesStep{
			Format:     "rpm",
			Phase: spec.PhaseInstall,
			Packages:   []string{"ripgrep"},
			RawInstallContext: map[string]any{
				"package": []any{"ripgrep"},
			},
		},
	}}
	if err := tgt.Emit([]*deploykit.InstallPlan{plan}, deploykit.EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "dnf install -y ripgrep") {
		t.Errorf("legacy template not rendered: %s", got)
	}
}

func TestOCITargetEmitSystemPackagesPrefersNewPhases(t *testing.T) {
	// Both legacy and new path set; new path must win.
	distro := &spec.ResolvedDistro{
		Format: map[string]*FormatDef{
			"rpm": {
				InstallTemplate: "RUN legacy-install\n",
				Phases: &vmshared.PhaseSet{
					Install: &vmshared.PhaseTemplates{
						Container: "RUN new-install {{join .Packages \" \"}}\n",
					},
				},
			},
		},
	}
	tgt := ociTestTarget(buildEngineContext{DistroCfg: buildkit.WrapDistroDef(distro)})
	plan := &deploykit.InstallPlan{Candy: "foo", Steps: []spec.InstallStep{
		&deploykit.SystemPackagesStep{
			Format:     "rpm",
			Phase: spec.PhaseInstall,
			Packages:   []string{"foo"},
			RawInstallContext: map[string]any{
				"package": []any{"foo"},
			},
		},
	}}
	if err := tgt.Emit([]*deploykit.InstallPlan{plan}, deploykit.EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "new-install foo") {
		t.Errorf("expected new phase template to win, got: %s", got)
	}
	if strings.Contains(got, "legacy-install") {
		t.Errorf("legacy template leaked despite new phases path: %s", got)
	}
}

// TestOCITargetEmitBuilderInlineViaPlugin drives the FULL real chain the C1.3 externalization
// introduces for an INLINE (cargo) builder: BuilderStep → deploykit.OCITarget.Emit → ociEmitStep →
// pluginEmitStepWords[Builder]="builder" → spliceClassStepEmit("builder") → the compiled-in
// candy/plugin-installstep OpEmit → emitViaHostBuild → HostBuild("step-emit",{Word:"builder"}) →
// stepEmitBuilder (the in-core host build engine on the in-proc reverse channel) → inline render.
// Since C10, an EXTERNALIZED inline builder (cargo) renders its InlineFragment via kit.BuilderResolve
// (no longer the embedded vocabulary install_template — the bDef needs only Inline:true), so this
// asserts kit's `cargo install --path /ctx` output. This is the exact in-proc chain a pod overlay
// with an inline-builder add_candy runs host-side.
func TestOCITargetEmitBuilderInlineViaPlugin(t *testing.T) {
	bc := &buildkit.BuilderConfig{Builder: map[string]*BuilderDef{
		"cargo": {Inline: true},
	}}
	gen := &Generator{Candies: map[string]*Candy{"mytool": {Name: "mytool"}}}
	tgt := ociTestTarget(buildEngineContext{BuilderConfig: bc, Box: &buildkit.ResolvedBox{UID: 1000, GID: 1000}, Generator: gen})
	plan := &deploykit.InstallPlan{Candy: "mytool", Steps: []spec.InstallStep{
		&deploykit.BuilderStep{Builder: "cargo", CandyName: "mytool", Phase: spec.PhaseInstall},
	}}
	if err := tgt.Emit([]*deploykit.InstallPlan{plan}, deploykit.EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "USER 1000") {
		t.Errorf("inline builder must switch USER to the image user via the plugin chain: %s", got)
	}
	if !strings.Contains(got, "cargo install --path /ctx") {
		t.Errorf("inline builder not rendered via the step:builder plugin chain + kit.BuilderResolve: %s", got)
	}
}

// TestOCITargetEmitBuilderMultiStageViaPlugin drives the FULL real chain for a MULTI-STAGE
// (pixi/npm/aur) builder. Same dispatch path as the inline test (through the compiled-in plugin's
// OpEmit and the in-proc HostBuild), proving stepEmitBuilder reaches the threaded
// Generator/BuilderConfig/Box build engine (buildEngineContext). Since C10, an EXTERNALIZED
// multi-stage builder (pixi) renders its stage via kit.BuilderResolve (no longer an embedded
// vocabulary stage template — the bDef needs only the "pixi" key present, the host still resolves the
// builder ref from Box.Builder), so this asserts kit's stage: the `FROM <builder> AS <stage>` line +
// the pixi cache-dir ENV line kit always emits.
func TestOCITargetEmitBuilderMultiStageViaPlugin(t *testing.T) {
	bc := &buildkit.BuilderConfig{Builder: map[string]*BuilderDef{
		"pixi": {},
	}}
	gen := &Generator{Candies: map[string]*Candy{"mytool": {Name: "mytool"}}}
	tgt := ociTestTarget(buildEngineContext{BuilderConfig: bc, Box: &buildkit.ResolvedBox{UID: 1000, GID: 1000, Builder: map[string]string{"pixi": "ghcr.io/x/builder:latest"}}, Generator: gen})
	plan := &deploykit.InstallPlan{Candy: "mytool", Steps: []spec.InstallStep{
		&deploykit.BuilderStep{Builder: "pixi", CandyName: "mytool", Phase: spec.PhaseInstall},
	}}
	if err := tgt.Emit([]*deploykit.InstallPlan{plan}, deploykit.EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "FROM ghcr.io/x/builder:latest AS mytool-pixi-build") {
		t.Errorf("multi-stage builder FROM stage not rendered via the step:builder plugin chain + kit.BuilderResolve: %s", got)
	}
	if !strings.Contains(got, "ENV PIXI_CACHE_DIR=/tmp/pixi-cache") {
		t.Errorf("multi-stage builder body not rendered via the step:builder plugin chain + kit.BuilderResolve: %s", got)
	}
}

// TestOCITargetEmitLocalPkgInstallViaPlugin drives the FULL real chain the C1.4 externalization
// introduces for a PRODUCTION localpkg install: LocalPkgInstallStep → deploykit.OCITarget.Emit → ociEmitStep →
// pluginEmitStepWords[LocalPkgInstall]="local-pkg-install" → spliceClassStepEmit("local-pkg-install") →
// the compiled-in candy/plugin-installstep OpEmit → emitViaHostBuild → HostBuild("step-emit",
// {Word:"local-pkg-install"}) → stepEmitLocalPkgInstall (the in-core host localpkg build engine on
// the in-proc reverse channel) → renderLocalPkgImageInstall. It asserts the release-download RUN the former
// in-proc overlay-walker localpkg build-emit produced — the test FAILS without this change (there is no
// in-proc LocalPkgInstall StepProvider; the plugin must serve step:local-pkg-install and the host must
// register the step-emit renderer). This is the exact in-proc chain a pod overlay with a localpkg
// add_candy runs host-side.
func TestOCITargetEmitLocalPkgInstallViaPlugin(t *testing.T) {
	lp := testPacLocalPkgDef()
	lp.DownloadTemplate = "https://github.com/opencharly/charly/releases/latest/download/opencharly-${ARCH}.pkg.tar.zst"
	tgt := ociTestTarget(buildEngineContext{Box: &buildkit.ResolvedBox{Name: "charly-arch"}})
	plan := &deploykit.InstallPlan{Candy: "charly", Steps: []spec.InstallStep{
		&deploykit.LocalPkgInstallStep{CandyName: "charly", Format: "pac", LocalPkg: lp},
	}}
	if err := tgt.Emit([]*deploykit.InstallPlan{plan}, deploykit.EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "curl -fsSL") || !strings.Contains(got, "releases/latest/download/opencharly-${ARCH}.pkg.tar.zst") {
		t.Errorf("production localpkg build-emit must DOWNLOAD the published release via the step:local-pkg-install plugin chain; got:\n%s", got)
	}
	if !strings.Contains(got, "pacman -U --noconfirm") {
		t.Errorf("production localpkg build-emit must install via the format install template via the plugin chain; got:\n%s", got)
	}
	if strings.Contains(got, "COPY ") {
		t.Errorf("production mode must NOT COPY a locally-built package; got:\n%s", got)
	}
}

// TestOCITargetEmitOpViaPlugin drives the FULL real chain the C1.5 externalization introduces for an
// Op (task) step — the RICHEST build-emit, which drives Generator.emitTasks: OpStep → deploykit.OCITarget.Emit →
// emitStep → pluginEmitStepWords[Op]="op" → spliceClassStepEmit("op") → the compiled-in
// candy/plugin-installstep OpEmit → emitViaHostBuild → HostBuild("step-emit",{Word:"op"}) → stepEmitOp
// (the in-core Generator.emitTasks engine on the in-proc reverse channel) → the per-verb emitters. It
// asserts both a RUN (mkdir) and a COPY (from the layer scratch stage) — the test FAILS without this
// change (there is no in-proc Op StepProvider after the cutover; the plugin must serve step:op and the
// host must register the step-emit renderer + thread Generator/Box/BuildDir/ContextRelPrefix onto the
// buildEngineContext). This is the exact in-proc chain a pod overlay with a run:/task add_candy runs
// host-side.
func TestOCITargetEmitOpViaPlugin(t *testing.T) {
	dir := t.TempDir()
	gen := &Generator{BuildDir: dir, Candies: map[string]*Candy{"mytool": {Name: "mytool"}}}
	tgt := ociTestTarget(buildEngineContext{Generator: gen, Box: testResolvedBox(), ImageBuildDir: dir, ContextRelPrefix: ".build/mytool"})
	plan := &deploykit.InstallPlan{Candy: "mytool", Steps: []spec.InstallStep{
		&deploykit.OpStep{Op: &spec.Op{Mkdir: "/opt/foo"}, CandyName: "mytool", ResolvedUser: "root"},
		&deploykit.OpStep{Op: &spec.Op{Copy: "bin/tool", To: "/opt/foo/tool"}, CandyName: "mytool", ResolvedUser: "root"},
	}}
	if err := tgt.Emit([]*deploykit.InstallPlan{plan}, deploykit.EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "RUN mkdir -p /opt/foo") {
		t.Errorf("mkdir op not rendered as a RUN via the step:op plugin chain:\n%s", got)
	}
	if !strings.Contains(got, "COPY --from=mytool") || !strings.Contains(got, "bin/tool") || !strings.Contains(got, "/opt/foo/tool") {
		t.Errorf("copy op not rendered as a COPY from the layer scratch stage via the step:op plugin chain:\n%s", got)
	}
}

func TestOCITargetSkipsVenueSkip(t *testing.T) {
	// A step with VenueSkip should be elided entirely.
	tgt := ociTestTarget(buildEngineContext{})
	plan := &deploykit.InstallPlan{Candy: "x", Steps: []spec.InstallStep{
		&fakeSkipStep{},
	}}
	if err := tgt.Emit([]*deploykit.InstallPlan{plan}, deploykit.EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if strings.Contains(got, "FAKE") {
		t.Errorf("skip step was rendered: %s", got)
	}
}

func TestOCITargetEmitRepoChange(t *testing.T) {
	tgt := ociTestTarget(buildEngineContext{})
	plan := &deploykit.InstallPlan{Candy: "rpmfusion", Steps: []spec.InstallStep{
		&deploykit.RepoChangeStep{
			Format:  "rpm",
			File:    "/etc/yum.repos.d/rpmfusion-free.repo",
			Content: "[rpmfusion-free]\nname=test",
		},
	}}
	if err := tgt.Emit([]*deploykit.InstallPlan{plan}, deploykit.EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "/etc/yum.repos.d/rpmfusion-free.repo") {
		t.Errorf("missing repo file path: %s", got)
	}
	if !strings.Contains(got, "[rpmfusion-free]") {
		t.Errorf("missing repo content: %s", got)
	}
}

// fakeSkipStep is a synthetic InstallStep used to verify VenueSkip
// elision. Returns Venue=VenueSkip and marker content in its Kind.
type fakeSkipStep struct{}

func (f *fakeSkipStep) Kind() spec.StepKind       { return "FAKE" }
func (f *fakeSkipStep) Scope() spec.Scope         { return spec.ScopeUser }
func (f *fakeSkipStep) Venue() spec.Venue         { return spec.VenueSkip }
func (f *fakeSkipStep) RequiresGate() spec.Gate   { return spec.GateNone }
func (f *fakeSkipStep) Reverse() []spec.ReverseOp { return nil }

// TestGeneratorCandyByNameRemoteQualifiedKey guards the add_candy-on-pod overlay
// build: a REMOTE add_candy candy (fetched via ResolveOpts.ExtraCandyRefs) is keyed
// in Generator.Candies under its fully-qualified ref, while the compiled plan step's
// CandyName is the candy's bare intrinsic name. candyByName (the step-emit Op/Builder
// path's candy resolver) must resolve the bare name to the qualified-key candy, or the
// OpStep build-emit fails with `task emit: candy "<name>" not found`. Regression for the
// add_candy-on-pod-overlay "candy not found" build failure.
func TestGeneratorCandyByNameRemoteQualifiedKey(t *testing.T) {
	gen := &Generator{Candies: map[string]*Candy{
		"github.com/org/repo/candy/marker": {Name: "marker"},
		"local-layer":                      {Name: "local-layer"},
	}}

	// Exact (local) key — bare == .Name — still resolves directly.
	if c := gen.candyByName("local-layer"); c == nil || c.Name != "local-layer" {
		t.Fatalf("local-layer: got %v, want .Name=local-layer", c)
	}
	// Bare name resolves the qualified-key remote candy (the regression this fix closes).
	if c := gen.candyByName("marker"); c == nil || c.Name != "marker" {
		t.Fatalf("marker bare-name lookup returned %v; qualified-key .Name fallback is broken", c)
	}
	// An unknown name is still nil (no accidental match).
	if c := gen.candyByName("nonexistent"); c != nil {
		t.Fatalf("nonexistent: want nil, got %v", c)
	}
	// A nil Generator is safe (returns nil).
	var nilGen *Generator
	if c := nilGen.candyByName("marker"); c != nil {
		t.Fatalf("nil Generator candyByName: want nil, got %v", c)
	}
}
