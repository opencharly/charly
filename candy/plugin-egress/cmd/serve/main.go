// Command serve is the OUT-OF-PROCESS entrypoint shim for the egress plugin. Egress is
// compiled-in in practice (the build/deploy hot paths + the perf-scoped build loader), so
// this exists for signature symmetry; the SAME provider compiles INTO charly via
// plugins_generated.go (M16).
package main

import (
	egress "github.com/opencharly/charly/candy/plugin-egress"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(egress.NewProvider(), egress.NewMeta()) }
