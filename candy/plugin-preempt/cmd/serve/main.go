// Command serve is the OUT-OF-PROCESS entrypoint for the preempt plugin: dual-mode sdk.Main
// (serve OR CLI). charly fork/execs this binary in CLI mode for command:preempt dispatch when the
// plugin is NOT compiled-in (→ CliMain); the serve half backs the out-of-process verb:arbiter
// placement. The SAME provider compiles INTO charly in-process (charly imports the parent package
// + registers NewProvider()/NewMeta() via plugins_generated.go) — placement-invisible.
package main

import (
	preempt "github.com/opencharly/charly/candy/plugin-preempt"
	"github.com/opencharly/charly/charly/plugin/sdk"
)

func main() { sdk.Main(preempt.NewProvider(), preempt.NewMeta(), preempt.CliMain) }
