// Package process is the importable, COMPILED-IN host-coupled `process` check verb:
// a `pgrep -x` exact-name match against the live deployment. It implements
// kit.CheckVerbProvider — RunVerb runs the probe via the live kit.CheckContext.
// Relocated out of charly's module (formerly charly/plugin/builtins/process +
// charly/plugin_process.go) onto the sdk/kit contract; COMPILED-IN-ONLY.
package process

import (
	"context"
	"embed"

	"github.com/opencharly/charly/candy/plugin-process/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewCheckVerb returns the process verb as a kit.CheckVerbProvider for compiled-in
// registration (charly's registerCompiledCheckVerb wraps it + registers the schema).
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

// NewMeta advertises verb:process (plugin_input #ProcessInput) + the embedded CUE schema,
// via sdk.NewMeta — the ONE meta both placements use (compiled-in registerCompiledCheckVerb
// reads it via Describe; cmd/serve serves it out-of-process), so a kit candy has the SAME
// NewCheckVerb()+NewMeta() shape as every pb-provider plugin (R3).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.1600",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "process", InputDef: "#ProcessInput"}},
		schemaFS)
}

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
