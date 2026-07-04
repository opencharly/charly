// Command serve is the OUT-OF-PROCESS entrypoint for the matching plugin: a thin shim
// serving the importable provider over go-plugin gRPC. The SAME provider compiles INTO
// charly in-process (charly imports the parent package + registers
// NewProvider()/NewMeta() via plugins_generated.go); this binary is built + connected
// only when the plugin is NOT in charly.yml compiled_plugins (the coexist path).
package main

import (
	matching "github.com/opencharly/charly/candy/plugin-matching"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(matching.NewProvider(), matching.NewMeta()) }
