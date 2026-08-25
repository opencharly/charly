// Command serve is the OUT-OF-PROCESS entrypoint for the skill/hook/marketplace harness-kind
// plugin: a thin shim serving the importable provider over go-plugin gRPC (the SAME provider
// compiles INTO charly in-process via plugins_generated.go, its default compiled-in placement).
package main

import (
	harnesskind "github.com/opencharly/plugin-harness-kind/candy/plugin-harness-kind"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(harnesskind.NewProvider(), harnesskind.NewMeta()) }
