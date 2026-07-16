package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// recordingExec is a DeployExecutor that records PutFile calls (destination +
// staged content) and returns configurable values for RunCapture/ResolveHome.
// GetFile reports not-found so managed-block writes start from empty.
type recordingExec struct {
	homeReturn       string
	runCaptureReturn string
	putDest          string
	putContent       string
	sysScripts       []string // RunSystem scripts (host-engine SystemPackages / act-OpStep arms)
	userScripts      []string // RunUser scripts
}

func (e *recordingExec) Venue() string { return "rec://test" }
func (e *recordingExec) RunSystem(_ context.Context, script string, _ deploykit.EmitOpts) error {
	e.sysScripts = append(e.sysScripts, script)
	return nil
}
func (e *recordingExec) RunUser(_ context.Context, script string, _ deploykit.EmitOpts) error {
	e.userScripts = append(e.userScripts, script)
	return nil
}
func (e *recordingExec) RunBuilder(context.Context, deploykit.BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (e *recordingExec) PutFile(_ context.Context, localPath, remotePath string, _ uint32, _ bool, _ deploykit.EmitOpts) error {
	e.putDest = remotePath
	b, _ := os.ReadFile(localPath)
	e.putContent = string(b)
	return nil
}
func (e *recordingExec) GetFile(context.Context, string, bool, deploykit.EmitOpts) ([]byte, error) {
	return nil, os.ErrNotExist
}
func (e *recordingExec) RunInteractive(context.Context, string) (int, error) {
	return -1, spec.ErrNotSupported
}
func (e *recordingExec) RunStream(context.Context, string) (int, error) {
	return -1, spec.ErrNotSupported
}
func (e *recordingExec) RunCapture(context.Context, string) (string, string, int, error) {
	return e.runCaptureReturn, "", 0, nil
}
func (e *recordingExec) Kind() string { return "rec" }
func (e *recordingExec) ResolveHome(context.Context, string) (string, error) {
	return e.homeReturn, nil
}

// D1: the compiler defers home — env.d values carry the {{.Home}} token, not a
// baked image home, so each deploy target resolves them against the real
// destination home at emit.
func TestCompileShellHookStepDefersHome(t *testing.T) {
	layer := &Candy{
		Name: "nodejs",
		envConfig: &kit.EnvConfig{
			Vars:       map[string]string{"NPM_CONFIG_PREFIX": "~/.npm-global"},
			PathAppend: []string{"$HOME/.npm-global/bin"},
		},
	}
	img := &buildkit.ResolvedBox{Home: "/home/operator"}
	step := deploykit.CompileShellHookStep(layer, img)
	if step == nil {
		t.Fatal("compileShellHookStep returned nil")
	}
	if got := step.EnvVars["NPM_CONFIG_PREFIX"]; got != "{{.Home}}/.npm-global" {
		t.Errorf("env value = %q, want token-deferred {{.Home}}/.npm-global (NOT baked img.Home)", got)
	}
	if got := step.PathAdd[0]; got != "{{.Home}}/.npm-global/bin" {
		t.Errorf("path_append = %q, want {{.Home}}/.npm-global/bin", got)
	}
	if strings.Contains(step.EnvVars["NPM_CONFIG_PREFIX"], "/home/operator") {
		t.Error("compile baked the image home into env.d — that's the VM $HOME bug")
	}
}

// D1: ResolveHome substitutes the token in every home-bearing field but leaves
// OpStep cmd bodies alone (those shell-expand $HOME at runtime as the deploy
// user, already correct on every venue). Idempotent.
func TestResolveHomeSubstitutesAcrossSteps(t *testing.T) {
	plan := &deploykit.InstallPlan{Steps: []spec.InstallStep{
		&deploykit.ShellHookStep{EnvVars: map[string]string{"P": "{{.Home}}/.npm-global"}, PathAdd: []string{"{{.Home}}/bin"}},
		&deploykit.ShellSnippetStep{Snippet: "export X={{.Home}}/y", Destination: "{{.Home}}/.bashrc", PathAppend: []string{"{{.Home}}/bin"}},
		&deploykit.FileStep{Dest: "{{.Home}}/.config/foo"},
		&deploykit.OpStep{Op: &spec.Op{Command: "echo {{.Home}}", Copy: "wrapper"}, To: "{{.Home}}/.local/bin/wrapper"},
	}}
	deploykit.ResolveHome(plan, "/home/cachy")

	sh := plan.Steps[0].(*deploykit.ShellHookStep)
	if sh.EnvVars["P"] != "/home/cachy/.npm-global" || sh.PathAdd[0] != "/home/cachy/bin" {
		t.Errorf("ShellHookStep not resolved: %+v", sh)
	}
	sn := plan.Steps[1].(*deploykit.ShellSnippetStep)
	if sn.Snippet != "export X=/home/cachy/y" || sn.Destination != "/home/cachy/.bashrc" || sn.PathAppend[0] != "/home/cachy/bin" {
		t.Errorf("ShellSnippetStep not resolved: %+v", sn)
	}
	fs := plan.Steps[2].(*deploykit.FileStep)
	if fs.Dest != "/home/cachy/.config/foo" {
		t.Errorf("FileStep.Dest = %q", fs.Dest)
	}
	ts := plan.Steps[3].(*deploykit.OpStep)
	if ts.Op.Command != "echo {{.Home}}" {
		t.Errorf("OpStep.Op.Command should be untouched (runtime $HOME), got %q", ts.Op.Command)
	}
	// The copy/download dest IS resolved — it's the PutFile target (single-quoted
	// under sudo, so it can't shell-expand). A literal "${HOME}" dest would make
	// PutFile create a "/home/cachy/${HOME}/..." dir under sudo (HOME=/root).
	if ts.To != "/home/cachy/.local/bin/wrapper" {
		t.Errorf("OpStep.To (copy dest) = %q, want /home/cachy/.local/bin/wrapper", ts.To)
	}

	// Idempotent: a second call (token already gone) is a no-op.
	deploykit.ResolveHome(plan, "/home/other")
	if sh.EnvVars["P"] != "/home/cachy/.npm-global" {
		t.Errorf("ResolveHome not idempotent: %q", sh.EnvVars["P"])
	}
}

// homeFailExec is a DeployExecutor whose ResolveHome ALWAYS fails — modeling a venue
// (a not-yet-created nested emulator pod, a cluster VM whose SSH isn't up) that is not
// reachable. resolveHomeCalls records whether prepareReverseState touched the venue.
type homeFailExec struct {
	recordingExec
	resolveHomeCalls int
}

func (e *homeFailExec) ResolveHome(context.Context, string) (string, error) {
	e.resolveHomeCalls++
	return "", context.DeadlineExceeded
}

// A plan carrying only an ApkInstallStep (an android device deploy) or otherwise no
// home-token / ServicePackaged step must NOT pay a live venue ResolveHome exec — that
// unconditional exec hard-failed (exit 125 "no such container" / exit 255 ssh) when the
// venue wasn't reachable yet. The guard skips it; a home-token plan still resolves.
func TestPrepareReverseState_SkipsVenueExecForApkOnlyPlan(t *testing.T) {
	// apk-only plan → guard skips ResolveHome entirely → no error even though the
	// venue exec would fail.
	apkExec := &homeFailExec{}
	tgt := &externalDeployTarget{name: "check-android-emulator-pod.device", exec: apkExec}
	apkPlan := &deploykit.InstallPlan{Steps: []spec.InstallStep{
		&deploykit.ApkInstallStep{Packages: []ApkPackageSpec{{Package: "com.example"}}, CandyName: "app"},
	}}
	if err := tgt.prepareReverseState(context.Background(), []*deploykit.InstallPlan{apkPlan}); err != nil {
		t.Fatalf("apk-only plan: prepareReverseState should skip the venue exec, got err: %v", err)
	}
	if apkExec.resolveHomeCalls != 0 {
		t.Errorf("apk-only plan called ResolveHome %d times, want 0 (guard must skip the unnecessary venue exec)", apkExec.resolveHomeCalls)
	}

	// A home-token plan (ShellHookStep) DOES need the home → ResolveHome is called, and
	// its failure surfaces (proving the guard didn't over-skip).
	homeExec := &homeFailExec{}
	tgt2 := &externalDeployTarget{name: "some-local", exec: homeExec}
	homePlan := &deploykit.InstallPlan{Steps: []spec.InstallStep{
		&deploykit.ShellHookStep{EnvVars: map[string]string{"P": "{{.Home}}/.npm"}, CandyName: "nodejs"},
	}}
	if err := tgt2.prepareReverseState(context.Background(), []*deploykit.InstallPlan{homePlan}); err == nil {
		t.Error("home-token plan: prepareReverseState should surface the ResolveHome failure, got nil")
	}
	if homeExec.resolveHomeCalls != 1 {
		t.Errorf("home-token plan called ResolveHome %d times, want 1", homeExec.resolveHomeCalls)
	}
}

// The env.d-sourcing managed block (written to the DESTINATION user's home) and the
// guest login-shell detection moved into the out-of-process kit.WalkPlans finalizer
// (kit.ensureVenueManagedBlock + kit.DetectShellFromPath) when target:vm externalized —
// the former in-proc managed-block writer + the in-proc VM-target guest-shell detection are gone, so
// their unit tests retired here; kit's render_test + the check-arch-vm bed cover the
// replacement (env.d sourced from the guest home on a live VM deploy).
