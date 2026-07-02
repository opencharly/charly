// Command serve is the OUT-OF-PROCESS entrypoint for the target kind plugin: a thin shim
// serving the importable provider over go-plugin gRPC (the SAME provider compiles INTO
// charly in-process via plugins_generated.go).
package main

import (
	targetkind "github.com/opencharly/charly/candy/plugin-target"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

func main() { sdk.Serve(targetkind.NewProvider(), targetkind.NewMeta()) }
