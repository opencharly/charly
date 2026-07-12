// Command serve is the OUT-OF-PROCESS entrypoint for the alias command plugin: dual-mode
// sdk.Main (serve OR CLI). charly fork/execs this binary in CLI mode for command:alias dispatch
// when the plugin is NOT compiled-in (→ CliMain); the serve half backs the out-of-process
// provider placement. The SAME NewProvider()/NewMeta() compile INTO charly in-process when listed
// in compiled_plugins — placement is invisible.
package main

import (
	alias "github.com/opencharly/charly/candy/plugin-alias"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(alias.NewProvider(), alias.NewMeta(), alias.CliMain) }
