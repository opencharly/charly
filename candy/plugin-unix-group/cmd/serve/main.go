// Command serve is the OUT-OF-PROCESS entrypoint for the unixgroup kit check verb (M1):
// a thin shim serving the importable verb over go-plugin gRPC via sdk.ServeCheckVerb,
// which reconstructs the kit.CheckContext from the host's reverse channel. The SAME
// verb compiles INTO charly in-process when listed in compiled_plugins; this binary is
// host-built + connected only when it is NOT — placement is invisible above the registry
// (the M1 completion: every compiled-in kit verb is now dual-placement).
package main

import (
	unixgroup "github.com/opencharly/charly/candy/plugin-unix-group"
	"github.com/opencharly/sdk"
)

func main() {
	sdk.ServeCheckVerb(unixgroup.NewCheckVerb(), "2026.176.2600", unixgroup.SchemaFS, unixgroup.SchemaDir, unixgroup.InputDefs)
}
