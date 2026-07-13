// Command serve is the OUT-OF-PROCESS entrypoint for the check command plugin: dual-mode
// sdk.Main (serve OR CLI). charly fork/execs this binary in CLI mode for command:check
// dispatch when the plugin is NOT compiled-in (→ CliMain, which errors because check needs
// the host reverse channel); the serve half backs the out-of-process provider placement. The
// SAME NewProvider()/NewMeta() compile INTO charly in-process when listed in compiled_plugins —
// placement is invisible.
package main

import (
	check "github.com/opencharly/charly/candy/plugin-check"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(check.NewProvider(), check.NewMeta(), check.CliMain) }
