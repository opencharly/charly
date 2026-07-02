// Command serve is the OUT-OF-PROCESS entrypoint for the builder kind plugin: a thin shim
// serving the importable provider over go-plugin gRPC (the SAME provider compiles INTO
// charly in-process via plugins_generated.go).
package main

import (
	builderkind "github.com/opencharly/charly/candy/plugin-builder"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

func main() { sdk.Serve(builderkind.NewProvider(), builderkind.NewMeta()) }
