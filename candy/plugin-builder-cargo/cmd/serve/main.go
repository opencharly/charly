// Command serve is the OUT-OF-PROCESS entrypoint for the buildercargo plugin: a thin shim
// serving the importable provider over go-plugin gRPC via sdk.Serve. The SAME
// NewProvider()/NewMeta() compile INTO charly in-process when listed in
// compiled_plugins; this binary is host-built + connected only when they are NOT —
// placement is invisible above the registry.
package main

import (
	buildercargo "github.com/opencharly/charly/candy/plugin-builder-cargo"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(buildercargo.NewProvider(), buildercargo.NewMeta()) }
