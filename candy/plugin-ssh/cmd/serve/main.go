// Command serve is the OUT-OF-PROCESS entrypoint for the ssh command plugin: dual-mode sdk.Main
// (serve OR CLI). charly fork/execs this binary in CLI mode for command:ssh dispatch when the
// plugin is NOT compiled-in (→ CliMain, which errors because ssh needs the host reverse channel to
// reach verb:libvirt); the serve half backs the out-of-process provider placement. The SAME
// NewProvider()/NewMeta() compile INTO charly in-process when listed in compiled_plugins.
package main

import (
	ssh "github.com/opencharly/plugin-ssh/candy/plugin-ssh"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(ssh.NewProvider(), ssh.NewMeta(), ssh.CliMain) }
