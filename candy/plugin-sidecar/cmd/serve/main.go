// Command serve is the OUT-OF-PROCESS entrypoint for the sidecar kind plugin: a thin shim
// serving the importable provider over go-plugin gRPC (the SAME provider compiles INTO
// charly in-process via plugins_generated.go).
package main

import (
	sidecarkind "github.com/opencharly/charly/candy/plugin-sidecar"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(sidecarkind.NewProvider(), sidecarkind.NewMeta()) }
