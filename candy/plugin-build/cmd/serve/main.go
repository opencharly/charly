// Command serve is the OUT-OF-PROCESS entrypoint for the build-engine dispatch plugin: a thin
// shim serving the importable provider over go-plugin gRPC (the SAME provider compiles INTO charly
// in-process via plugins_generated.go, its default compiled-in placement).
package main

import (
	build "github.com/opencharly/charly/candy/plugin-build"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

func main() { sdk.Serve(build.NewProvider(), build.NewMeta()) }
