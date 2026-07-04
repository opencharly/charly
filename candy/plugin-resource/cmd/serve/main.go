// Command serve is the OUT-OF-PROCESS entrypoint for the resource kind plugin: a thin shim
// serving the importable provider over go-plugin gRPC (the SAME provider compiles INTO
// charly in-process via plugins_generated.go).
package main

import (
	resourcekind "github.com/opencharly/charly/candy/plugin-resource"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(resourcekind.NewProvider(), resourcekind.NewMeta()) }
