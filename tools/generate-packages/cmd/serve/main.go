// Command serve is the OUT-OF-PROCESS entrypoint for the generate-packages plugin.
//
// This module is a THIN RE-EXPORT SHIM. The provider itself lives in the external
// module github.com/opencharly/plugin-generate-packages/candy/generate-packages;
// nothing is reimplemented here. The shim exists so the superproject can DECLARE the
// `generate-packages` command (candy/generate-packages/charly.yml `plugin.providers`)
// and have the host build a real binary for it from a directory inside this repo —
// the plugin candy's `source:` field is identity metadata, not a fetch instruction, so
// the build resolves through this module's own go.mod require + replace.
package main

import (
	generatepackages "github.com/opencharly/plugin-generate-packages/candy/generate-packages"
	"github.com/opencharly/sdk"
)

func main() {
	sdk.Main(generatepackages.NewProvider(), generatepackages.NewMeta(), generatepackages.CliMain)
}
