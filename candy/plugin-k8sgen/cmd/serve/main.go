// Command serve is the OUT-OF-PROCESS entrypoint shim for the k8sgen plugin.
// k8sgen is compiled-in in practice (the deploy / from-box paths call the in-core
// GenerateK8sKustomize shim, which Invokes verb:k8sgen in-proc), so this exists for
// signature symmetry; the SAME provider compiles INTO charly via
// plugins_generated.go (C8/M13).
package main

import (
	k8sgen "github.com/opencharly/charly/candy/plugin-k8sgen"
	"github.com/opencharly/sdk"
)

func main() { sdk.Serve(k8sgen.NewProvider(), k8sgen.NewMeta()) }
