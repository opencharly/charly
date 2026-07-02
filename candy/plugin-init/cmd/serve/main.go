// Command serve is the OUT-OF-PROCESS entrypoint for the init kind plugin: a thin shim
// serving the importable provider over go-plugin gRPC. The SAME provider compiles INTO
// charly in-process (NewProvider()/NewMeta() via plugins_generated.go); this binary is
// built + connected only when the plugin is NOT in charly.yml compiled_plugins.
package main

import (
	initkind "github.com/opencharly/charly/candy/plugin-init"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

func main() { sdk.Serve(initkind.NewProvider(), initkind.NewMeta()) }
