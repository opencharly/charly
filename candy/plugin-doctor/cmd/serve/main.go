// Command serve is the OUT-OF-PROCESS entrypoint for the doctor command plugin: dual-mode
// sdk.Main (serve OR CLI). charly fork/execs this binary in CLI mode for command:doctor
// dispatch when the plugin is NOT compiled-in (→ CliMain); the serve half backs the
// out-of-process provider placement. The SAME NewProvider()/NewMeta() compile INTO
// charly in-process when listed in compiled_plugins — placement is invisible.
package main

import (
	doctor "github.com/opencharly/charly/candy/plugin-doctor"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(doctor.NewProvider(), doctor.NewMeta(), doctor.CliMain) }
