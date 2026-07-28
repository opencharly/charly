// Package clean is the charly plugin OWNING the externalized `charly clean` command — the
// build-artifact retention/prune surface — AND (K1-alpha core-minimization) the SHARED retention
// ENGINE itself (retention.go: image-tag / build-candy / check-run pruning + the --deep store-wide
// dangling-image purge + the charly-labeled image-tag inventory). The plugin owns the flag
// grammar, the category orchestration, the output, AND the engine; the ONE thing it genuinely
// cannot compute is the project's defaults.keep_images/keep_check_runs (needs the core LoadConfig
// loader), reached via the small "retention-defaults" HostBuild seam — the doctrine the vm + pod
// deploy plugins established (own the work, call back only for the one core-coupled piece).
//
// Two capabilities: command:clean (the CLI) and verb:retention (the engine, invoked by peer
// plugins — candy/plugin-box's `box list tags`/post-build prune (listImageTags/pruneAfterBuild,
// #118) and candy/plugin-check's post-run prune — over InvokeProvider, the SAME peer-dispatch
// pattern verb:credential/verb:gpu/verb:tunnel use; NO core adapter remains (charly/
// retention_plugin.go, the former core-side listCharlyImageTags caller, is DELETED). clean is
// COMPILED-IN (charly.yml compiled_plugins): command:clean's Invoke(OpRun)
// (provider.go) runs in charly's process and gets the in-proc reverse channel that
// dispatchInProcCommand threads (Seam A), so HostBuild("retention-defaults") reaches the host. The
// out-of-process placement fork/execs the binary → CliMain, which has NO reverse channel, so the
// categories needing a resolved keep-default (images/check/deep) error there; list/invalidate need
// no default and work standalone. NewProvider()/NewMeta()/CliMain are the standard dual-mode
// command shape (mirror candy/plugin-migrate); NewMeta advertises both words so the compiled-in
// registry path (registerCompiledPlugin → resolve(class,word) → Invoke) dispatches either.
package clean

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// NewProvider returns the clean provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:clean (the CLI, dispatched via dispatchInProcCommand → Invoke(OpRun)
// with the threaded in-proc reverse channel) and verb:retention (the engine, invoked directly by
// core adapters / peer plugins — no authored plugin_input, mirroring verb:credential) — plus the
// self-contained doc schema, via sdk.NewMeta.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.181.0001",
		[]sdk.ProvidedCapability{
			{Class: "command", Word: "clean"},
			{Class: "verb", Word: "retention"},
		},
		nil)
}

// CliMain is the out-of-process CLI entrypoint (only reached when clean is NOT compiled in). The
// categories that need a resolved keep-default (images/check/deep) reach fetchRetentionDefaults'
// HostBuild call, which is unavailable out-of-process and errors clearly there; list/invalidate
// need no default and run standalone. The canonical placement is compiled-in (Invoke →
// provider.go), where the reverse channel is threaded.
func CliMain(args []string) int {
	if err := runCleanCLI(context.Background(), nil, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
