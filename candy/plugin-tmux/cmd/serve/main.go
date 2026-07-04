// Command serve is the OUT-OF-PROCESS entrypoint for the tmux command plugin: dual-mode
// sdk.Main (serve OR CLI). charly fork/execs this binary in CLI mode for command:tmux
// dispatch when the plugin is NOT compiled-in (→ CliMain); the serve half backs the
// out-of-process provider placement. The SAME NewProvider()/NewMeta() compile INTO
// charly in-process when listed in compiled_plugins — placement is invisible.
package main

import (
	tmux "github.com/opencharly/charly/candy/plugin-tmux"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(tmux.NewProvider(), tmux.NewMeta(), tmux.CliMain) }
