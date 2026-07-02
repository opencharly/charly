// Command serve is the OUT-OF-PROCESS entrypoint for the externalprobe plugin: a
// thin shim that serves the importable provider over go-plugin gRPC. The SAME
// provider compiles INTO charly in-process (charly imports the parent package and
// registers NewProvider()/NewMeta()); this binary is built + connected only when
// the plugin is NOT compiled into the running charly (the coexist path —
// loadProjectPlugins builds it on the host and connects via LocalTransport).
package main

import (
	externalprobe "github.com/opencharly/charly/candy/plugin-example-external"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

func main() { sdk.Serve(externalprobe.NewProvider(), externalprobe.NewMeta()) }
