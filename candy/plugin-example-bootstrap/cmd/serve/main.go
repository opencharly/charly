// Command serve is the OUT-OF-PROCESS entrypoint for the examplebootstrap plugin (the dual-mode
// shim). Bootstrap plugins are compiled-in in practice (no validated config exists yet to discover
// an out-of-process source), so the serve half exists for signature symmetry; the SAME provider
// compiles INTO charly via plugins_generated.go (F9).
package main

import (
	examplebootstrap "github.com/opencharly/charly/candy/plugin-example-bootstrap"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

func main() { sdk.Serve(examplebootstrap.NewProvider(), examplebootstrap.NewMeta()) }
