package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// testOCITarget is a thin, byte-faithful local port of sdk/deploykit.OCITarget (oci_target.go)
// — the kind-blind Containerfile walker's OWN logic is pure string assembly over spec-native
// types (spec.InstallPlan/InstallStep/spec.ResolveHome), with zero engine coupling of its own;
// its correctness is ALREADY separately covered by sdk/deploykit/oci_target_test.go (e.g.
// TestOCITarget_EmitElidesVenueSkipAndEmptyFragments). This file's OWN tests exercise the REAL
// charly-side dispatch (ociEmitStep → dispatchOCIStep → candy/plugin-installstep) through the
// SAME EmitStepOp seam shape the real deploykit.OCITarget wires in production — only the walker
// harness driving that dispatch is ported locally, never the dispatch itself.
type testOCITarget struct {
	Home       string
	Distros    []string
	EmitStepOp func(step spec.InstallStep, plan *spec.InstallPlan, distros []string) (string, error)
	buf        strings.Builder
}

func (t *testOCITarget) Emit(plans []*spec.InstallPlan, _ spec.EmitOpts) error {
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		if t.Home != "" {
			spec.ResolveHome(plan, t.Home)
		}
		fmt.Fprintf(&t.buf, "# Layer: %s\n", plan.Candy)
		for _, step := range plan.Steps {
			if step == nil || step.Venue() == spec.VenueSkip {
				continue
			}
			var frag string
			var err error
			if t.EmitStepOp != nil {
				frag, err = t.EmitStepOp(step, plan, t.Distros)
			}
			if err != nil {
				return fmt.Errorf("testOCITarget.Emit(%s): %w", plan.Candy, err)
			}
			if frag == "" {
				continue
			}
			t.buf.WriteString(frag)
			if !strings.HasSuffix(frag, "\n") {
				t.buf.WriteString("\n")
			}
		}
		t.buf.WriteString("\n")
	}
	return nil
}

func (t *testOCITarget) String() string { return t.buf.String() }

// Tests for the pod-overlay step-emit dispatch (charly/oci_step_emit.go's ociEmitStep — the
// Go-object-typed test entry point after the P11c overlay-walker relocation to sdk/deploykit; the
// dispatch DECISION itself relocated further, into candy/plugin-installstep's "oci-dispatch" word,
// K5-A item 2). The former core overlay walker struct is GONE (the kind-blind walker now lives in
// sdk/deploykit/oci_target.go as deploykit.OCITarget); these tests exercise the REAL dispatch
// through the SAME seam the candy uses in production: a deploykit.OCITarget whose EmitStepOp
// delegates to ociEmitStep → dispatchOCIStep → the plugin's "oci-dispatch". The walker's
// `# Layer:` headers + home resolution are preserved (mirrors the former in-core overlay walker
// Emit); the per-step fragment is byte-identical to the pre-relocation render (the dispatch's
// OBSERVABLE behavior is unchanged; only its placement moved).

// ociTestTarget constructs a deploykit.OCITarget wired to the ociEmitStep dispatch over the
// given host buildEngineContext, so the tests exercise the real dispatch through the production
// seam (deploykit.OCITarget.EmitStepOp → HostBuild("step-emit","oci-emit-step") → ociEmitStep).
// Home/Distros are empty (the tests that need home resolution or per-step distros are rare; add a
// dedicated constructor if one arises).
// stubResolvedProject swaps the compiled-in "build:project" PROVIDER (candy/plugin-build's
// InvokeProvider("build","project",sdk.OpResolve,...) — the relocated home of the former
// resolved-project host seam, #55 step3 unit 3b) for one whose Invoke returns rp verbatim,
// restoring the original registration on test cleanup. The 4 former HOST-COUPLED step-emit words
// (system-packages/builder/local-pkg-install/op, K5-Unit-6b) no longer read the synthetic
// buildEngineContext's Generator/Box/BuilderConfig/DistroCfg fields directly — candy/plugin-installstep
// fetches a real resolved-project envelope (now via InvokeProvider, not HostBuild) and renders
// against it instead, so a test that needs to feed it project structure (a resolved box, its
// distro/builder vocab, a candy) does so by stubbing the PROVIDER the registry resolves "build:project"
// to, exactly like a real project load would populate it. providerRegistry.register() fail-fasts on a
// duplicate (class, word), so the swap manipulates providerRegistry.byKey/origins directly (same
// pattern registry_testsupport_test.go's snapshotProviderState already uses for the same map) rather
// than going through register(). The per-invocation scalars (Image/DevLocalPkg/ImageBuildDir/
// ContextRelPrefix) still ride the buildEngineContext passed to ociTestTarget, unchanged.
func stubResolvedProject(t *testing.T, rp spec.ResolvedProject) {
	t.Helper()
	out, err := json.Marshal(rp)
	if err != nil {
		t.Fatalf("stubResolvedProject: marshal: %v", err)
	}
	key := provKey(ClassBuild, "project")
	providerRegistry.mu.Lock()
	orig, hadOrig := providerRegistry.byKey[key]
	origOrigin := providerRegistry.origins[key]
	providerRegistry.byKey[key] = stubBuildProjectProvider{json: out}
	providerRegistry.origins[key] = "test-stub"
	providerRegistry.mu.Unlock()
	t.Cleanup(func() {
		providerRegistry.mu.Lock()
		if hadOrig {
			providerRegistry.byKey[key] = orig
			providerRegistry.origins[key] = origOrigin
		} else {
			delete(providerRegistry.byKey, key)
			delete(providerRegistry.origins, key)
		}
		providerRegistry.mu.Unlock()
	})
}

// stubBuildProjectProvider is the fake "build:project" Provider stubResolvedProject installs: its
// Invoke ignores the request entirely and returns the fixed marshaled spec.ResolvedProject, mirroring
// the former hostBuilders["resolved-project"] stub func's unconditional return.
type stubBuildProjectProvider struct{ json json.RawMessage }

func (stubBuildProjectProvider) Reserved() string     { return "project" }
func (stubBuildProjectProvider) Class() ProviderClass { return ClassBuild }
func (p stubBuildProjectProvider) Invoke(_ context.Context, _ *Operation) (*Result, error) {
	return &Result{JSON: p.json}, nil
}

// chdirTemp creates a fresh temp dir, chdirs into it for the test's duration (restored via
// t.Cleanup), and returns its path. candy/plugin-installstep caches its "resolved-project"-built
// *deploykit.Generator by os.Getwd() (genCache) — a test stubbing its OWN synthetic envelope needs
// its OWN unique cwd, or the FIRST stubbed test to populate that cache key would leak its Generator
// into every other stubbed test sharing the SAME (un-chdir'd) process cwd.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return dir
}

// stubRenderGen is GONE (K-wave 2 cone R1): it seeded the render-seam host-builder's per-dir
// Generator cache, and both that builder and the cache are deleted — no render seam calls back to
// the host any more.

func ociTestTarget(build buildEngineContext) *testOCITarget {
	return &testOCITarget{
		EmitStepOp: func(step spec.InstallStep, plan *spec.InstallPlan, d []string) (string, error) {
			return ociEmitStep(step, plan, d, build)
		},
	}
}

func TestOCITargetEmitShellHook(t *testing.T) {
	tgt := ociTestTarget(buildEngineContext{})
	plan := &spec.InstallPlan{Candy: "uv", Steps: []spec.InstallStep{
		&spec.ShellHookStep{
			CandyName: "uv",
			EnvVars: map[string]string{
				"UV_INSTALL_DIR": "/usr/local/bin",
			},
			PathAdd: []string{"$HOME/.cargo/bin"},
		},
	}}
	if err := tgt.Emit([]*spec.InstallPlan{plan}, spec.EmitOpts{}); err != nil {
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
	chdirTemp(t)
	// Legacy InstallTemplate set; PhaseTemplate returns it for (install, container).
	distro := &spec.ResolvedDistro{
		Format: map[string]*spec.Format{
			"rpm": {
				InstallTemplate: "RUN dnf install -y {{join .Packages \" \"}}\n",
			},
		},
	}
	stubResolvedProject(t, spec.ResolvedProject{
		Distro: map[string]*spec.ResolvedDistro{"test-distro": distro},
		Boxes:  map[string]spec.ResolvedBoxView{"ripgrep-box": {Name: "ripgrep-box", Distro: []string{"test-distro"}}},
	})
	tgt := ociTestTarget(buildEngineContext{Box: &spec.ResolvedBox{Name: "ripgrep-box"}})
	plan := &spec.InstallPlan{Candy: "ripgrep", Steps: []spec.InstallStep{
		&spec.SystemPackagesStep{
			Format:   "rpm",
			Phase:    spec.PhaseInstall,
			Packages: []string{"ripgrep"},
			RawInstallContext: map[string]any{
				"package": []any{"ripgrep"},
			},
		},
	}}
	if err := tgt.Emit([]*spec.InstallPlan{plan}, spec.EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "dnf install -y ripgrep") {
		t.Errorf("legacy template not rendered: %s", got)
	}
}

func TestOCITargetEmitSystemPackagesPrefersNewPhases(t *testing.T) {
	chdirTemp(t)
	// Both legacy and new path set; new path must win.
	distro := &spec.ResolvedDistro{
		Format: map[string]*spec.Format{
			"rpm": {
				InstallTemplate: "RUN legacy-install\n",
				Phases: &spec.PhaseSet{
					Install: &spec.PhaseTemplates{
						Container: "RUN new-install {{join .Packages \" \"}}\n",
					},
				},
			},
		},
	}
	stubResolvedProject(t, spec.ResolvedProject{
		Distro: map[string]*spec.ResolvedDistro{"test-distro": distro},
		Boxes:  map[string]spec.ResolvedBoxView{"foo-box": {Name: "foo-box", Distro: []string{"test-distro"}}},
	})
	tgt := ociTestTarget(buildEngineContext{Box: &spec.ResolvedBox{Name: "foo-box"}})
	plan := &spec.InstallPlan{Candy: "foo", Steps: []spec.InstallStep{
		&spec.SystemPackagesStep{
			Format:   "rpm",
			Phase:    spec.PhaseInstall,
			Packages: []string{"foo"},
			RawInstallContext: map[string]any{
				"package": []any{"foo"},
			},
		},
	}}
	if err := tgt.Emit([]*spec.InstallPlan{plan}, spec.EmitOpts{}); err != nil {
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

// TestOCITargetEmitBuilderInlineViaPlugin drives the FULL real chain for an INLINE (cargo)
// builder: BuilderStep → deploykit.OCITarget.Emit → ociEmitStep → dispatchOCIStep →
// candy/plugin-installstep's "oci-dispatch" → pluginEmitStepWords[Builder]="builder" →
// InvokeProvider("step","builder") → the compiled-in candy/plugin-installstep ops.OpEmit
// → the plugin's OWN "resolved-project"-built deploykit.Generator (stubResolvedProject feeds the
// synthetic project structure) → inline render. An EXTERNALIZED inline builder (cargo) renders its
// InlineFragment via kit.BuilderResolve (the bDef needs only Inline:true), so this asserts kit's
// `cargo install --path /ctx` output. This is the exact chain a pod overlay with an inline-builder
// add_candy runs.
func TestOCITargetEmitBuilderInlineViaPlugin(t *testing.T) {
	chdirTemp(t)
	stubResolvedProject(t, spec.ResolvedProject{
		Builder:              map[string]*spec.Builder{"cargo": {Inline: true}},
		ExternalizedBuilders: map[string]bool{"cargo": true},
		Boxes:                map[string]spec.ResolvedBoxView{"mytool-box": {Name: "mytool-box", UID: 1000, GID: 1000}},
		CandyModels:          map[string]spec.CandyModel{"mytool": {Name: "mytool"}},
		Candies:              map[string]spec.CandyView{"mytool": {}},
	})
	tgt := ociTestTarget(buildEngineContext{Box: &spec.ResolvedBox{Name: "mytool-box", UID: 1000, GID: 1000}})
	plan := &spec.InstallPlan{Candy: "mytool", Steps: []spec.InstallStep{
		&spec.BuilderStep{Builder: "cargo", CandyName: "mytool", Phase: spec.PhaseInstall},
	}}
	if err := tgt.Emit([]*spec.InstallPlan{plan}, spec.EmitOpts{}); err != nil {
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
// ops.OpEmit rendering directly against its resolved-project-built Generator), proving the plugin's
// emitBuilder reaches dg.BuildStageContext with the box/candy the stubbed envelope carries. An
// EXTERNALIZED multi-stage builder (pixi) renders its stage via kit.BuilderResolve (the bDef needs
// only the "pixi" key present, the box's own Builder map resolves the builder ref), so this asserts
// kit's stage: the `FROM <builder> AS <stage>` line + the pixi cache-dir ENV line kit always emits.
func TestOCITargetEmitBuilderMultiStageViaPlugin(t *testing.T) {
	chdirTemp(t)
	stubResolvedProject(t, spec.ResolvedProject{
		Builder:              map[string]*spec.Builder{"pixi": {}},
		ExternalizedBuilders: map[string]bool{"pixi": true},
		Boxes: map[string]spec.ResolvedBoxView{"mytool-box": {
			Name: "mytool-box", UID: 1000, GID: 1000, Builder: map[string]string{"pixi": "ghcr.io/x/builder:latest"},
		}},
		CandyModels: map[string]spec.CandyModel{"mytool": {Name: "mytool"}},
		Candies:     map[string]spec.CandyView{"mytool": {}},
	})
	tgt := ociTestTarget(buildEngineContext{Box: &spec.ResolvedBox{Name: "mytool-box", UID: 1000, GID: 1000, Builder: map[string]string{"pixi": "ghcr.io/x/builder:latest"}}})
	plan := &spec.InstallPlan{Candy: "mytool", Steps: []spec.InstallStep{
		&spec.BuilderStep{Builder: "pixi", CandyName: "mytool", Phase: spec.PhaseInstall},
	}}
	if err := tgt.Emit([]*spec.InstallPlan{plan}, spec.EmitOpts{}); err != nil {
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

// TestOCITargetEmitLocalPkgInstallViaPlugin drives the FULL real chain for a PRODUCTION localpkg
// install: LocalPkgInstallStep → deploykit.OCITarget.Emit → ociEmitStep → dispatchOCIStep →
// candy/plugin-installstep's "oci-dispatch" → pluginEmitStepWords[LocalPkgInstall]="local-pkg-install"
// → InvokeProvider("step","local-pkg-install") → the compiled-in candy/plugin-installstep ops.OpEmit →
// deploykit.RenderLocalPkgImageInstall, called
// DIRECTLY (a pure function of the step + the BuildEnv scalars — no resolved-project envelope
// needed at all for this word). It asserts the release-download RUN the former in-proc
// overlay-walker localpkg build-emit produced. This is the exact chain a pod overlay with a
// localpkg add_candy runs.
func TestOCITargetEmitLocalPkgInstallViaPlugin(t *testing.T) {
	lp := testPacLocalPkgDef()
	lp.DownloadTemplate = "https://github.com/opencharly/charly/releases/latest/download/opencharly-${ARCH}.pkg.tar.zst"
	tgt := ociTestTarget(buildEngineContext{Box: &spec.ResolvedBox{Name: "charly-arch"}})
	plan := &spec.InstallPlan{Candy: "charly", Steps: []spec.InstallStep{
		&spec.LocalPkgInstallStep{CandyName: "charly", Format: "pac", LocalPkg: lp},
	}}
	if err := tgt.Emit([]*spec.InstallPlan{plan}, spec.EmitOpts{}); err != nil {
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

// TestOCITargetEmitOpViaPlugin drives the FULL real chain for an Op (task) step — the RICHEST
// build-emit, which drives Generator.EmitTasks: OpStep → deploykit.OCITarget.Emit → ociEmitStep →
// dispatchOCIStep → candy/plugin-installstep's "oci-dispatch" → pluginEmitStepWords[Op]="op" →
// InvokeProvider("step","op") → the compiled-in
// candy/plugin-installstep ops.OpEmit → the plugin's OWN "resolved-project"-built deploykit.Generator
// (stubResolvedProject feeds the synthetic box+candy) → dg.EmitTasks → the per-verb emitters. It
// asserts both a RUN (mkdir) and a COPY (from the layer scratch stage). ImageBuildDir/
// ContextRelPrefix (the inline-content staging anchor) ride the BuildEnv scalars from the
// buildEngineContext passed to ociTestTarget, unchanged from before this cutover.
func TestOCITargetEmitOpViaPlugin(t *testing.T) {
	// testResolvedBox() reads fixtures relative to the package's testdata dir — capture it BEFORE
	// chdirTemp changes the process cwd for the plugin's resolved-project cache-key isolation.
	box := testResolvedBox()
	chdirTemp(t)
	dir := t.TempDir()
	stubResolvedProject(t, spec.ResolvedProject{
		Boxes: map[string]spec.ResolvedBoxView{"test-img": {
			Name: "test-img", UID: 1000, GID: 1000, Home: "/home/user", User: "user",
		}},
		CandyModels: map[string]spec.CandyModel{"mytool": {Name: "mytool"}},
		Candies:     map[string]spec.CandyView{"mytool": {}},
	})
	tgt := ociTestTarget(buildEngineContext{Box: &box.ResolvedBox, ImageBuildDir: dir, ContextRelPrefix: ".build/mytool"})
	plan := &spec.InstallPlan{Candy: "mytool", Steps: []spec.InstallStep{
		&spec.OpStep{Op: &spec.Op{Mkdir: "/opt/foo"}, CandyName: "mytool", ResolvedUser: "root"},
		&spec.OpStep{Op: &spec.Op{Copy: "bin/tool", To: "/opt/foo/tool"}, CandyName: "mytool", ResolvedUser: "root"},
	}}
	if err := tgt.Emit([]*spec.InstallPlan{plan}, spec.EmitOpts{}); err != nil {
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

// TestOCITargetSkipsVenueSkip was removed as a duplicate (K3 cone2 test closure):
// VenueSkip elision is walker-level behavior (deploykit.OCITarget.Emit), already
// proven in isolation by sdk/deploykit/oci_target_test.go's
// TestOCITarget_EmitElidesVenueSkipAndEmptyFragments — same assertion (a
// VenueSkip step never reaches the rendered output), verified live before
// deletion.

func TestOCITargetEmitRepoChange(t *testing.T) {
	tgt := ociTestTarget(buildEngineContext{})
	plan := &spec.InstallPlan{Candy: "rpmfusion", Steps: []spec.InstallStep{
		&spec.RepoChangeStep{
			Format:  "rpm",
			File:    "/etc/yum.repos.d/rpmfusion-free.repo",
			Content: "[rpmfusion-free]\nname=test",
		},
	}}
	if err := tgt.Emit([]*spec.InstallPlan{plan}, spec.EmitOpts{}); err != nil {
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

// TestGeneratorCandyByNameRemoteQualifiedKey MOVED to candy/plugin-deploy-pod
// (candy_by_name_test.go, K-wave 2 cone R1) with the function it guards: charly's
// Generator.candyByName had been production-dead since the overlay render relocated, and the live
// twin in candy/plugin-deploy-pod/overlay.go carried no test of its own until the move.
