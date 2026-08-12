// Command serve is the OUT-OF-PROCESS placement shim for the agentteams
// command plugin: `charly` fork/execs this binary with the pass-through tokens
// after `charly agentteams` when the plugin is served out-of-process. It runs
// the SAME effect as the compiled-in Invoke(OpRun) path (CliMain), so both
// placements are placement-invisible.
package main

import (
	"github.com/opencharly/sdk"

	agentteams "github.com/opencharly/charly/candy/plugin-agentteams"
)

func main() {
	sdk.Main(agentteams.NewProvider(), agentteams.NewMeta(), agentteams.CliMain)
}
