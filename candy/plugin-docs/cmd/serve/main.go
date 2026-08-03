// Command serve is the OUT-OF-PROCESS entrypoint for the docs command plugin: dual-mode
// sdk.Main (serve OR CLI). charly fork/execs this binary in CLI mode for command:docs dispatch
// — the canonical placement here, since the docs generator is a dev-time tool deliberately kept
// out of compiled_plugins and therefore out of every shipped charly binary.
package main

import (
	docs "github.com/opencharly/charly/candy/plugin-docs"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(docs.NewProvider(), docs.NewMeta(), docs.CliMain) }
