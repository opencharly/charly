package fleet

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// deploy_home_test.go — relocated (in part) from charly/deploy_home_test.go (#55 decoupling,
// Batch A): TestCompileShellHookStepDefersHome asserts deploykit.CompileShellHookStep directly,
// zero charly coupling. TestResolveHomeSubstitutesAcrossSteps (asserts spec.ResolveHome, already
// SPEC) plus the recordingExec DeployExecutor fixture stay in charly — recordingExec is also
// used by charly's plugin_executor_hoststep_test.go and vm_builder_xhost_test.go, neither in
// this decoupling batch.

// D1: the compiler defers home — env.d values carry the {{.Home}} token, not a
// baked image home, so each deploy target resolves them against the real
// destination home at emit.
func TestCompileShellHookStepDefersHome(t *testing.T) {
	layer := testCandy("nodejs", spec.CandyModel{
		Env: &kit.EnvConfig{
			Vars:       map[string]string{"NPM_CONFIG_PREFIX": "~/.npm-global"},
			PathAppend: []string{"$HOME/.npm-global/bin"},
		},
	}, spec.CandyView{})
	img := &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Home: "/home/operator"}}
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
