// Package ssh is the COMPILED-IN charly COMMAND-class plugin owning the externalized
// `charly ssh tunnel spice/vnc` command (#118 loader+check-tail cone). It opens an SSH-forwarded
// local SPICE/VNC endpoint pointing at a VM's display on a remote libvirt host.
//
// ssh is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) runs in charly's process and
// gets the in-proc reverse channel (dispatchInProcCommand threads it), so it reaches verb:libvirt
// (go-libvirt, out-of-process) via InvokeProvider for the display-endpoint resolve — the ONE thing
// it cannot do itself. The out-of-process CliMain path has no reverse channel and so errors. The
// SAME NewProvider()/NewMeta() compile INTO charly in-process — placement is invisible. It imports
// ONLY the sdk module, never charly core.
package ssh

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
)

// NewProvider returns the ssh command provider (command:ssh).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:ssh — the compiled-in registry path resolves it and dispatches
// Invoke(OpRun) with the threaded in-proc reverse channel. command:ssh is input-less (its args are
// plain CLI tokens kong-parsed into the SshCmd tree), so it ships NO schema. Subcommands is derived
// from SshCmd's OWN Kong tags via sdk.KongSubcommands (F-CLI-NEST) so `charly ssh --help` lists the
// nested tunnel spice/vnc subcommands.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.209.0000",
		[]sdk.ProvidedCapability{{Class: "command", Word: "ssh", Subcommands: sdk.KongSubcommands(&SshCmd{})}},
		nil)
}

// CliMain is the out-of-process CLI entrypoint (only reached when ssh is NOT compiled in). ssh
// reaches verb:libvirt via the HostBuild/InvokeProvider reverse channel, which is unavailable
// out-of-process, so it errors clearly; the canonical placement is compiled-in.
func CliMain(_ []string) int {
	fmt.Fprintln(os.Stderr, "charly ssh requires compiled-in placement (the vm-plugin reverse channel is unavailable out-of-process)")
	return 1
}
