// Command serve is the OUT-OF-PROCESS entrypoint for the clean command plugin: dual-mode
// sdk.Main (serve OR CLI). charly fork/execs this binary in CLI mode for command:clean
// dispatch when the plugin is NOT compiled-in (→ CliMain); the serve half backs the
// out-of-process provider placement. The SAME NewProvider()/NewMeta() compile INTO
// charly in-process when listed in compiled_plugins — placement is invisible.
package main

import (
	clean "github.com/opencharly/charly/candy/plugin-clean"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(clean.NewProvider(), clean.NewMeta(), clean.CliMain) }
