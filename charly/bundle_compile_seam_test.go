package main

import (
	"os"
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// bundle_compile_seam_test.go — the P13-KERNEL step-3 systemd-hardcode fix's seam-path coverage.
// install_build_test.go exercises deploykit.BuildDeployPlan directly (the pure compiler), entirely
// bypassing bundle_compile_seam.go's per-whole-deploy selection computers (compileBoxSelection /
// compileCandySelection) — zero prior coverage of the by-name, existence-checked active-init
// preresolve added there. These tests exercise that seam file's own new code directly.

// TestResolveActiveInitByName_MissingSystemd_HardErrors proves the fixed behavior: a MachineVenue
// compile whose build vocabulary declares NO "systemd" init entry hard-errors (naming "systemd")
// instead of the original bug — compileServiceSteps' lazy loadSystemd() silently swallowing the
// absence with no error at all, leaving every custom service step's UnitText/UnitPath empty. This
// function did not exist before the fix, so this test fails to even compile against the
// pre-fix tree — the honest signature of "zero prior coverage of this exact seam path".
func TestResolveActiveInitByName_MissingSystemd_HardErrors(t *testing.T) {
	initCfg := &InitConfig{Init: map[string]*ResolvedInit{
		"supervisord": {ManagementTool: "supervisorctl"},
	}}
	name, def, err := resolveActiveInitByName(initCfg)
	if err == nil {
		t.Fatalf("expected a hard error for a build vocabulary with no systemd init entry; got name=%q def=%v", name, def)
	}
	if !strings.Contains(err.Error(), "systemd") {
		t.Errorf("error must name the missing init system, got: %q", err.Error())
	}
	if name != "" || def != nil {
		t.Errorf("expected zero-value (name, def) on error, got (%q, %v)", name, def)
	}
}

// TestResolveActiveInitByName_NilInitConfig_HardErrors covers the "no init: section at all" case
// (a build vocabulary lacking even the map) — must ALSO hard-error, never a nil-deref.
func TestResolveActiveInitByName_NilInitConfig_HardErrors(t *testing.T) {
	name, def, err := resolveActiveInitByName(nil)
	if err == nil {
		t.Fatalf("expected a hard error for a nil InitConfig; got name=%q def=%v", name, def)
	}
	if !strings.Contains(err.Error(), "systemd") {
		t.Errorf("error must name the missing init system, got: %q", err.Error())
	}
}

// TestResolveActiveInitByName_SystemdPresent_Resolves proves the happy path: a build vocabulary
// that DOES declare "systemd" resolves it by name, verbatim, with no error.
func TestResolveActiveInitByName_SystemdPresent_Resolves(t *testing.T) {
	want := &ResolvedInit{ManagementTool: "systemctl"}
	initCfg := &InitConfig{Init: map[string]*ResolvedInit{"systemd": want}}
	name, def, err := resolveActiveInitByName(initCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "systemd" || def != want {
		t.Errorf("resolveActiveInitByName = (%q, %p), want (\"systemd\", %p)", name, def, want)
	}
}

// TestPreresolveActiveInitInto_ContainerVenue_NoOp proves the container-image compile path
// (hostCtx.MachineVenue == false, the pod-overlay / OCI target) is entirely untouched by the
// MachineVenue-only preresolve — no project load, no error, hostCtx returned unchanged.
func TestPreresolveActiveInitInto_ContainerVenue_NoOp(t *testing.T) {
	in := deploykit.HostContext{MachineVenue: false, Distro: "fedora:43"}
	out, err := preresolveActiveInitInto(in, "/nonexistent/does/not/matter")
	if err != nil {
		t.Fatalf("container-venue compile must never touch the project loader: %v", err)
	}
	if out.ActiveInitName != "" || out.ActiveInit != nil {
		t.Errorf("expected zero-value ActiveInitName/ActiveInit for a container venue, got %q/%v", out.ActiveInitName, out.ActiveInit)
	}
}

// TestPreresolveActiveInitInto_MachineVenue_ResolvesSystemd is the seam-path integration proof:
// against the REAL project's build vocabulary (which declares systemd), a MachineVenue compile's
// preresolve populates hostCtx.ActiveInitName/ActiveInit — exactly what
// compileBoxSelection/compileCandySelection now do before every deploy-mode compile.
func TestPreresolveActiveInitInto_MachineVenue_ResolvesSystemd(t *testing.T) {
	isolateProviderRegistry(t)
	dir, cleanup := compilerTestProjectDir(t)
	defer cleanup()

	out, err := preresolveActiveInitInto(deploykit.HostContext{MachineVenue: true}, dir)
	if err != nil {
		t.Fatalf("preresolveActiveInitInto: %v", err)
	}
	if out.ActiveInitName != "systemd" {
		t.Errorf("ActiveInitName = %q, want \"systemd\"", out.ActiveInitName)
	}
	if out.ActiveInit == nil {
		t.Fatal("ActiveInit must be populated for a real project's systemd entry")
	}
}

// TestCompileServiceSteps_PrefersPreresolvedActiveInit closes the loop end-to-end:
// compileServiceSteps (install_build_services.go) must consume the seam-preresolved
// hostCtx.ActiveInit, not silently fall through to its own lazy os.Getwd()-based lookup — proven
// by moving cwd to an empty temp dir (no charly.yml at all, so the fallback lookup would return
// false and leave UnitText empty) BEFORE calling compileServiceSteps. A rendered UnitText can only
// have come from the preresolved value.
func TestCompileServiceSteps_PrefersPreresolvedActiveInit(t *testing.T) {
	isolateProviderRegistry(t)
	dir, cleanupProject := compilerTestProjectDir(t)
	hostCtx, err := preresolveActiveInitInto(deploykit.HostContext{MachineVenue: true}, dir)
	cleanupProject()
	if err != nil {
		t.Fatalf("preresolveActiveInitInto: %v", err)
	}
	if hostCtx.ActiveInitName != "systemd" || hostCtx.ActiveInit == nil {
		t.Fatalf("expected preresolved systemd, got %q/%v", hostCtx.ActiveInitName, hostCtx.ActiveInit)
	}

	prevWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	emptyDir := t.TempDir()
	if err := os.Chdir(emptyDir); err != nil {
		t.Fatalf("chdir %s: %v", emptyDir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	layer := testCandy("sentinel-candy", spec.CandyModel{Service: []spec.ServiceEntry{
		{Name: "sentinel", Exec: "/usr/bin/true", Enable: true, Scope: "system"},
	}}, spec.CandyView{})
	img := &buildkit.ResolvedBox{Name: "sentinel-box", Distro: []string{"fedora:43", "fedora"}}

	steps := compileServiceSteps(layer, img, hostCtx)
	var custom *deploykit.ServiceCustomStep
	for _, s := range steps {
		if cs, ok := s.(*deploykit.ServiceCustomStep); ok {
			custom = cs
		}
	}
	if custom == nil {
		t.Fatal("expected one ServiceCustomStep for the sentinel entry")
	}
	if custom.UnitText == "" {
		t.Fatal("expected rendered UnitText from the PRERESOLVED ActiveInit — the lazy os.Getwd() fallback has no charly.yml to find from the empty temp cwd")
	}
}
