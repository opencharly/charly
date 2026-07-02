// Command serve is the OUT-OF-PROCESS entrypoint for the http kit check verb (F2): a thin
// shim that serves the importable verb over go-plugin gRPC via sdk.ServeCheckVerb, which
// reconstructs the kit.CheckContext from the host's reverse channel (ExecutorService +
// CheckContextService). The SAME verb compiles INTO charly in-process when listed in
// compiled_plugins; this binary is host-built + connected only when it is NOT (the coexist
// path) — placement is invisible above the registry.
package main

import (
	httpverb "github.com/opencharly/charly/candy/plugin-http"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

func main() {
	sdk.ServeCheckVerb(httpverb.NewCheckVerb(), "2026.176.2200", httpverb.SchemaFS, httpverb.SchemaDir, httpverb.InputDefs)
}
