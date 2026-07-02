// Command serve is the OUT-OF-PROCESS entrypoint for the tunnel plugin: a thin shim
// that serves the importable provider over go-plugin gRPC. The SAME provider compiles
// INTO charly in-process (charly imports the parent package and registers
// NewProvider()/NewMeta() via the generated plugins_generated.go, since plugin-tunnel is
// listed in charly.yml compiled_plugins:); this binary is built + connected only when the
// plugin is NOT compiled into the running charly (the coexist path — loadProjectPlugins
// builds it on the host and connects via LocalTransport).
package main

import (
	tunnelverb "github.com/opencharly/charly/candy/plugin-tunnel"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

func main() { sdk.Serve(tunnelverb.NewProvider(), tunnelverb.NewMeta()) }
