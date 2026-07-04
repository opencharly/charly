// Command serve is the OUT-OF-PROCESS entrypoint for the examplecommand plugin: dual-mode
// sdk.Main (serve OR CLI). charly fork/execs this binary in CLI mode for command dispatch
// (→ CliMain); the serve half exists only for the dual-mode signature. The SAME provider
// compiles INTO charly in-process (charly imports the parent package + registers
// NewProvider()/NewMeta() via plugins_generated.go) — placement-invisible (F8).
package main

import (
	examplecommand "github.com/opencharly/charly/candy/plugin-example-command"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(examplecommand.NewProvider(), examplecommand.NewMeta(), examplecommand.CliMain) }
