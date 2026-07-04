// Command serve is the OUT-OF-PROCESS entrypoint for the port kit check verb (F2): a thin
// shim that serves the importable verb over go-plugin gRPC via sdk.ServeCheckVerb, which
// reconstructs the kit.CheckContext from the host's reverse channel (ExecutorService for
// Exec; the Mode/DialTimeout scalars from the env snapshot). The SAME verb compiles INTO
// charly in-process when listed in compiled_plugins; this binary is host-built + connected
// only when it is NOT (the coexist path) — placement is invisible above the registry.
package main

import (
	port "github.com/opencharly/charly/candy/plugin-port"
	"github.com/opencharly/sdk"
)

func main() {
	sdk.ServeCheckVerb(port.NewCheckVerb(), "2026.176.1500", port.SchemaFS, port.SchemaDir, port.InputDefs)
}
