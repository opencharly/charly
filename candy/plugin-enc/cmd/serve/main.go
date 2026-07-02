// Command serve is the OUT-OF-PROCESS entrypoint shim for the enc plugin. enc is
// compiled-in in practice (charly config mount/unmount/passwd + charly start's ensure
// hook all call the in-core shim, which Invokes verb:enc in-proc), so this exists for
// signature symmetry; the SAME provider compiles INTO charly via plugins_generated.go
// (C16a).
package main

import (
	enc "github.com/opencharly/charly/candy/plugin-enc"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

func main() { sdk.Serve(enc.NewProvider(), enc.NewMeta()) }
