// Command serve is the OUT-OF-PROCESS entrypoint for the package-group kind plugin: a thin shim
// serving the importable provider over go-plugin gRPC (the SAME provider compiles INTO
// charly in-process via plugins_generated.go).
package main

import (
	packagegroupkind "github.com/opencharly/charly/candy/plugin-package-group"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

func main() { sdk.Serve(packagegroupkind.NewProvider(), packagegroupkind.NewMeta()) }
