// Command serve is the OUT-OF-PROCESS entrypoint for the marketplace command plugin: charly
// syscall.Exec's this binary in CLI mode (sdk.Main → CliMain) on the first actual
// `charly marketplace …` invocation.
package main

import (
	marketplace "github.com/opencharly/plugin-marketplace/candy/plugin-marketplace"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(marketplace.NewProvider(), marketplace.NewMeta(), marketplace.CliMain) }
