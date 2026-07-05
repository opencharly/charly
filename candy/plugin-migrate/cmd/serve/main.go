// Command serve is the OUT-OF-PROCESS entrypoint for the migrate command plugin: dual-mode
// sdk.Main (serve OR CLI). charly fork/execs this binary in CLI mode for command:migrate
// dispatch when the plugin is NOT compiled-in (→ CliMain); the serve half backs the
// out-of-process provider placement. The SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins (the canonical placement — migrate must resolve
// independent of any config) — placement is invisible above the registry.
package main

import (
	migrate "github.com/opencharly/charly/candy/plugin-migrate"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(migrate.NewProvider(), migrate.NewMeta(), migrate.CliMain) }
