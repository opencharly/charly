// Command serve is the OUT-OF-PROCESS placement shim for the dsh plugin: `charly`
// fork/execs this binary with the pass-through tokens after `charly dsh` when the
// plugin is served out-of-process. It runs the SAME effect as the compiled-in
// Invoke(OpRun) path (CliMain) — though the command's pod-lifecycle reverse
// channel is unavailable out-of-process, so command leaves fail with the
// plugin-cmd "no host reverse channel" error (compiled-in placement is required);
// the verb placement is placement-invisible.
package main

import (
	"github.com/opencharly/sdk"

	dsh "github.com/opencharly/plugin-dsh/candy/plugin-dsh"
)

func main() {
	sdk.Main(dsh.NewProvider(), dsh.NewMeta(), dsh.CliMain)
}
