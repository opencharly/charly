// Package examplerunverb is the importable, COMPILED-IN reference HOST-COUPLED check
// verb: it echoes plugin_input.marker AND a fact read off the live engine (the run
// mode), proving a verb whose RunVerb reaches the live kit.CheckContext relocates into
// a candy and still dispatches in-process. The *Runner-keeping analogue of the
// stateless candy/plugin-example exampleprobe. Relocated out of charly's module
// (formerly charly/plugin/builtins/examplerunverb + charly/plugin_examplerunverb.go)
// onto the charly/plugin/kit contract; COMPILED-IN-ONLY.
package examplerunverb

import (
	"context"
	"embed"

	"github.com/opencharly/charly/candy/plugin-examplerunverb/params"
	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir.
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:examplerunverb": "#ExamplerunverbInput"}

// NewCheckVerb returns the examplerunverb verb as a kit.CheckVerbProvider for compiled-in registration.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "examplerunverb" }

// RunVerb returns a deterministic pass echoing plugin_input.marker AND the live run
// mode (read off the CheckContext — proving the verb reaches engine state an
// out-of-process Invoke could not). Mirrors the former r-keeping RunVerb.
func (verb) RunVerb(_ context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.ExamplerunverbInput
	kit.DecodeInput(op.PluginInput, &in)
	marker := in.Marker
	if marker == "" {
		marker = "examplerunverb-ok"
	}
	return kit.Passf("%s (mode=%s)", marker, cc.Mode())
}
