// Command serve is the OUT-OF-PROCESS entrypoint for the box command plugin: dual-mode sdk.Main
// (serve OR CLI). charly fork/execs this binary in CLI mode for the nested box commands when the
// plugin is NOT compiled-in (→ CliMain, which errors: the generate/validate/pkg handlers need the
// host reverse channel, unavailable out-of-process). The serve half backs the out-of-process
// provider placement. The SAME NewProvider()/NewMeta() compile INTO charly in-process when listed
// in compiled_plugins — placement is invisible.
package main

import (
	box "github.com/opencharly/charly/candy/plugin-box"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(box.NewProvider(), box.NewMeta(), box.CliMain) }
