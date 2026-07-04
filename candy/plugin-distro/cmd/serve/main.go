// Command serve is the OUT-OF-PROCESS entrypoint for the distro kind plugin: a thin shim
// serving the importable provider over go-plugin gRPC (the SAME provider compiles INTO
// charly in-process via plugins_generated.go).
package main

import (
	distrokind "github.com/opencharly/charly/candy/plugin-distro"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(distrokind.NewProvider(), distrokind.NewMeta()) }
