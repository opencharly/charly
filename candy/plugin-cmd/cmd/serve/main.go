// Command serve is the OUT-OF-PROCESS entrypoint for the cmd command plugin: dual-mode sdk.Main
// (serve OR CLI). charly fork/execs this binary in CLI mode for command:cmd dispatch when the
// plugin is NOT compiled-in (→ CliMain, which errors because cmd needs the host reverse channel to
// drive the __cmd deploy-lifecycle reentry); the serve half backs the out-of-process provider
// placement. The SAME NewProvider()/NewMeta() compile INTO charly in-process when listed in
// compiled_plugins.
package main

import (
	cmd "github.com/opencharly/charly/candy/plugin-cmd"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(cmd.NewProvider(), cmd.NewMeta(), cmd.CliMain) }
