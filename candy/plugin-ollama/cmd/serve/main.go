// Command serve is the OUT-OF-PROCESS entrypoint for the ollama command plugin: dual-mode
// sdk.Main (serve OR CLI). charly fork/execs this binary in CLI mode for command:ollama
// dispatch when the plugin is NOT compiled-in (→ CliMain); the serve half backs the
// out-of-process provider placement. The SAME NewProvider()/NewMeta() compile INTO
// charly in-process when listed in compiled_plugins — placement is invisible.
package main

import (
	ollama "github.com/opencharly/plugin-ollama/candy/plugin-ollama"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(ollama.NewProvider(), ollama.NewMeta(), ollama.CliMain) }
