// Package process is the importable, COMPILED-IN host-coupled `process` check verb:
// a `pgrep -x` exact-name match against the live deployment. It implements
// kit.CheckVerbProvider — RunVerb runs the probe via the live kit.CheckContext.
// Relocated out of charly's module (formerly charly/plugin/builtins/process +
// charly/plugin_process.go) onto the charly/plugin/kit contract; COMPILED-IN-ONLY.
package process

import (
	"context"
	"embed"

	"github.com/opencharly/charly/candy/plugin-process/params"
	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir.
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:process": "#ProcessInput"}

// NewCheckVerb returns the process verb as a kit.CheckVerbProvider for compiled-in
// registration (charly's registerCompiledCheckVerb wraps it + registers the schema).
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "process" }

// RunVerb runs the pgrep probe via the live CheckContext. The process name + optional
// running expectation come from plugin_input (params.ProcessInput). Mirrors the former
// r.runProcess exactly.
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.ProcessInput
	kit.DecodeInput(op.PluginInput, &in)

	wantRunning := true
	if in.Running != nil {
		wantRunning = *in.Running
	}
	probe := "pgrep -x " + kit.ShellQuote(in.Process) + " >/dev/null 2>&1"
	_, _, exit, err := cc.Exec().RunCapture(ctx, probe)
	if err != nil {
		return kit.Failf("probe: %v", err)
	}
	isRunning := exit == 0
	if isRunning != wantRunning {
		return kit.Failf("running=%v, want %v", isRunning, wantRunning)
	}
	return kit.Passf("running=%v", isRunning)
}
