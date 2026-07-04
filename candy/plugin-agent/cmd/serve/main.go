// Command serve is the OUT-OF-PROCESS entrypoint for the agent kind plugin: a thin shim
// serving the importable provider over go-plugin gRPC (the SAME provider compiles INTO
// charly in-process via plugins_generated.go).
package main

import (
	agentkind "github.com/opencharly/charly/candy/plugin-agent"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(agentkind.NewProvider(), agentkind.NewMeta()) }
