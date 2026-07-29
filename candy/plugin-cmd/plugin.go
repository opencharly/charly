// Package cmd is the COMPILED-IN charly COMMAND-class plugin owning the externalized `charly cmd`
// command (#118 loader+check-tail cone) — run a single command in a running container with an
// optional desktop notification.
//
// cmd is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) runs in charly's process and
// gets the in-proc reverse channel (dispatchInProcCommand threads it), so it drives the "pod-cmd"
// host-builder (the deploy-lifecycle-coupled interactive exec a plugin cannot perform —
// dispatchLifecycleTarget + LifecycleTarget.Attach, cmd's slot in the floored pod-lifecycle-dispatch
// family) with host-held exec.RunInteractive stdio, and sends the completion notification itself.
// The out-of-process CliMain path has no reverse channel and so errors. The SAME NewProvider()/
// NewMeta() compile INTO charly in-process — placement is invisible. It imports ONLY the sdk module,
// never charly core.
package cmd

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
)

// NewProvider returns the cmd command provider (command:cmd).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:cmd — the compiled-in registry path resolves it and dispatches
// Invoke(OpRun) with the threaded in-proc reverse channel. command:cmd is a FLAT command (Box +
// Command positionals, no subcommand catalog), so it declares no Subcommands and ships no schema.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.209.0000",
		[]sdk.ProvidedCapability{{Class: "command", Word: "cmd"}},
		nil)
}

// CliMain is the out-of-process CLI entrypoint (only reached when cmd is NOT compiled in). cmd
// drives the "pod-cmd" deploy-lifecycle host-builder over the in-proc reverse channel, which is
// unavailable out-of-process, so it errors clearly; the canonical placement is compiled-in.
func CliMain(_ []string) int {
	fmt.Fprintln(os.Stderr, "charly cmd requires compiled-in placement (the pod-cmd deploy-lifecycle reverse channel is unavailable out-of-process)")
	return 1
}
