// Command serve is the OUT-OF-PROCESS entrypoint for the module kind plugin: a thin shim
// serving the importable provider over go-plugin gRPC (the SAME provider compiles INTO
// charly in-process via plugins_generated.go).
package main

import (
	modulekind "github.com/opencharly/charly/candy/plugin-module"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(modulekind.NewProvider(), modulekind.NewMeta()) }
