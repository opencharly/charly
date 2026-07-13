// Command serve is the OUT-OF-PROCESS entrypoint shim for the oci plugin. oci is
// compiled-in in practice (charly's in-core oci_plugin.go shim + the build drive Invoke
// verb:oci; the merge + adopt-user probes sit on the core build path and must resolve
// project-lessly), so this exists for signature symmetry + the coexist path when a custom
// build omits it from compiled_plugins:; the SAME provider compiles INTO charly via the
// generated plugins_generated.go.
package main

import (
	oci "github.com/opencharly/charly/candy/plugin-oci"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(oci.NewProvider(), oci.NewMeta()) }
