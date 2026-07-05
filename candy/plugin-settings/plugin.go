// Package settings is the charly plugin OWNING the externalized `charly settings` command — the
// runtime-configuration get/set/list/path/reset surface. The plugin owns the subcommand grammar + the
// output; the config subsystem (read/write ~/.config/charly/config.yml via GetConfigValue /
// SetConfigValue / ListConfigValues / ResetConfigValue / RuntimeConfigPath, the credential-store
// backend, and the runtime-engine resolution) stays in core and is reached via the generic "settings"
// HostBuild seam. There is no hidden core-command forward — the plugin does the work, calling back for
// the config ops it can't perform (it has no host config subsystem), the doctrine the vm + pod deploy
// plugins and command:clean established.
//
// settings is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) (provider.go) runs in
// charly's process and gets the in-proc reverse channel that dispatchInProcCommand threads (Seam A), so
// HostBuild("settings") reaches the host config subsystem. The out-of-process placement fork/execs the
// binary → CliMain, which has NO reverse channel and so errors — settings cannot run out-of-process (it
// needs the settings host seam). NewProvider()/NewMeta()/CliMain are the standard dual-mode command
// shape (mirror candy/plugin-migrate + command:clean); NewMeta advertises command:settings so the
// compiled-in registry path (registerCompiledPlugin → resolve(ClassCommand,"settings") →
// dispatchInProcCommand) dispatches it.
package settings

import (
	"context"
	"embed"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the settings provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:settings — the COMPILED-IN registry path resolves it
// (registerCompiledPlugin → resolve(ClassCommand,"settings") → dispatchInProcCommand → Invoke(OpRun)
// with the threaded in-proc reverse channel) — plus the self-contained doc schema, via sdk.NewMeta.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.181.0001",
		[]sdk.ProvidedCapability{{Class: "command", Word: "settings"}},
		schemaFS)
}

// CliMain is the out-of-process CLI entrypoint (only reached when settings is NOT compiled in). settings
// reaches the config subsystem via the HostBuild reverse channel, which is unavailable out-of-process,
// so runSettingsCLI (with a nil executor) errors clearly; the canonical placement is compiled-in
// (Invoke → provider.go), where the reverse channel is threaded.
func CliMain(args []string) int {
	if err := runSettingsCLI(context.Background(), nil, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
