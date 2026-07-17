// Command serve is the OUT-OF-PROCESS entrypoint for the substrate structural kind plugin: a
// dual-mode shim (sdk.Main — serve OR CLI) serving the importable provider over go-plugin gRPC
// (the SAME provider compiles INTO charly in-process via plugins_generated.go, its default
// compiled-in placement). The CLI half backs command:reap-orphans dispatch when this candy is NOT
// compiled in (→ CliMain, which errors — reap-orphans needs the reverse-channel executor,
// unavailable out-of-process).
package main

import (
	substratekind "github.com/opencharly/charly/candy/plugin-substrate"
	"github.com/opencharly/sdk"
)

func main() {
	sdk.Main(substratekind.NewProvider(), substratekind.NewMeta(), substratekind.CliMain)
}
