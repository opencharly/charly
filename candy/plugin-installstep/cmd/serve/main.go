// Command serve is the OUT-OF-PROCESS entrypoint for the installstep plugin: a thin shim that
// serves the importable provider over go-plugin gRPC. The SAME provider compiles INTO charly
// in-process (charly imports the parent package and registers NewProvider()/NewMeta() via the
// generated plugins_generated.go); this binary is built + connected only when the plugin is NOT
// compiled into the running charly (the coexist path — loadProjectPlugins builds it on the host and
// connects via LocalTransport).
package main

import (
	installstep "github.com/opencharly/charly/candy/plugin-installstep"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

func main() { sdk.Serve(installstep.NewProvider(), installstep.NewMeta()) }
