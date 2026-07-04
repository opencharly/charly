// Command serve is the OUT-OF-PROCESS entrypoint for the examplestep example plugin: a thin
// shim serving the importable provider over go-plugin gRPC via sdk.Serve. The SAME
// NewProvider()/NewMeta() compile INTO charly in-process when listed in
// compiled_plugins; this binary is host-built + connected only when they are NOT —
// placement is invisible above the registry.
package main

import (
	examplestep "github.com/opencharly/charly/candy/plugin-example-step"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(examplestep.NewProvider(), examplestep.NewMeta()) }
