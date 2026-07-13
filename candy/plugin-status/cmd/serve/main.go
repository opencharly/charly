// Command serve is the OUT-OF-PROCESS entrypoint for the status command plugin: dual-mode
// sdk.Main (serve OR CLI). charly fork/execs this binary in CLI mode for command:status
// dispatch when the plugin is NOT compiled-in (→ CliMain, which errors — status needs the
// status-substrate host seam, unavailable out-of-process); the serve half backs the
// out-of-process provider placement. The SAME NewProvider()/NewMeta() compile INTO
// charly in-process when listed in compiled_plugins — placement is invisible.
package main

import (
	status "github.com/opencharly/charly/candy/plugin-status"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(status.NewProvider(), status.NewMeta(), status.CliMain) }
