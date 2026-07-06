// Package candy is the charly plugin OWNING the externalized `charly candy` command — the
// candy-manifest authoring surface (set / add-rpm / add-deb / add-pac / add-aur). The plugin owns the
// ENTIRE logic: the subcommand grammar AND the comment-preserving yaml.Node mutation of
// candy/<name>/charly.yml. The only shared pieces are the GENERIC yaml utilities kit.SetByDotPath /
// kit.MappingChild (also used by `charly box set` / `charly box scaffold`); there is no core candy
// logic and no HostBuild seam — a plugin editing yaml owns that itself. Because the logic is
// self-contained, `charly candy` works identically compiled-in OR out-of-process (like migrate).
//
// NOTE: this is the TOP-LEVEL `charly candy` authoring tree — NOT `charly new candy` (NewCandyCmd, a
// child of `charly new`), which is a different command and stays a builtin.
//
// candy is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) (provider.go) runs in charly's
// process and runs runCandyCLI directly (no reverse channel needed — the yaml mutation is host-local
// file work). The out-of-process placement fork/execs the binary → CliMain, which runs the SAME
// runCandyCLI. NewProvider()/NewMeta()/CliMain are the standard dual-mode command shape; NewMeta
// advertises command:candy so the compiled-in registry path (registerCompiledPlugin →
// resolve(ClassCommand,"candy") → dispatchInProcCommand) dispatches it.
package candy

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// NewProvider returns the candy provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:candy — the COMPILED-IN registry path resolves it (registerCompiledPlugin
// → resolve(ClassCommand,"candy") → dispatchInProcCommand → Invoke(OpRun)) — plus the self-contained
// doc schema, via sdk.NewMeta.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.181.0001",
		[]sdk.ProvidedCapability{{Class: "command", Word: "candy"}},
		nil)
}

// CliMain is the CLI entrypoint (the out-of-process placement + the shared entry). candy is
// self-contained, so it runs runCandyCLI directly — no reverse channel, works in either placement.
func CliMain(args []string) int {
	if err := runCandyCLI(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
