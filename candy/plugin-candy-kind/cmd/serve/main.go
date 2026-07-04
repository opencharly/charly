// Command serve is the OUT-OF-PROCESS entrypoint for the candy box⊻layer factory kind plugin: a
// thin shim serving the importable provider over go-plugin gRPC (the SAME provider compiles INTO
// charly in-process via plugins_generated.go, its default compiled-in placement).
package main

import (
	candykind "github.com/opencharly/charly/candy/plugin-candy-kind"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(candykind.NewProvider(), candykind.NewMeta()) }
