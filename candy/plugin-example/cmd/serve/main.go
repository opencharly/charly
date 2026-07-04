// Command serve is the OUT-OF-PROCESS entrypoint for the exampleprobe plugin: a thin
// shim serving the importable provider over go-plugin gRPC. The SAME provider compiles
// INTO charly in-process (charly imports the parent package + registers
// NewProvider()/NewMeta() via plugins_generated.go); this binary is built + connected
// only when the plugin is NOT in charly.yml compiled_plugins (the coexist path).
package main

import (
	exampleprobe "github.com/opencharly/charly/candy/plugin-example"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(exampleprobe.NewProvider(), exampleprobe.NewMeta()) }
