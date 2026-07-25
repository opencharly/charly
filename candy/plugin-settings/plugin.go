// Package settings is the charly plugin OWNING the externalized `charly settings` command — the
// runtime-configuration get/set/list/path/reset surface. The plugin owns BOTH the subcommand
// grammar/output AND the config subsystem itself (config.go, wave γ): read/write
// ~/.config/charly/config.yml (GetConfigValue/SetConfigValue/ListConfigValues/ResetConfigValue/
// kit.RuntimeConfigPath) is pure sdk/kit file I/O; the credential-store touches (vnc.password.*,
// secret_backend) + runtime-engine resolution dispatch verb:credential / call sdk/kit directly. No
// core round-trip — the former generic "settings" HostBuild seam is retired.
//
// settings is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) (provider.go) runs in
// charly's process and gets the in-proc reverse channel that dispatchInProcCommand threads (Seam A),
// giving InvokeProvider capability for the credential-touching ops. The out-of-process placement
// fork/execs the binary → CliMain, which has NO reverse channel; CliMain refuses unconditionally
// (never partially works) rather than exposing a silently narrower out-of-process capability set —
// settings' supported, tested placement stays compiled-in-only, exactly as before this cutover.
// NewProvider()/NewMeta()/CliMain are the standard dual-mode command shape (mirror
// candy/plugin-migrate + command:clean); NewMeta advertises command:settings so the compiled-in
// registry path (registerCompiledPlugin → resolve(ClassCommand,"settings") → dispatchInProcCommand)
// dispatches it.
package settings

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

// NewProvider returns the settings provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises command:settings — the COMPILED-IN registry path resolves it
// (registerCompiledPlugin → resolve(ClassCommand,"settings") → dispatchInProcCommand → Invoke(OpRun)
// with the threaded in-proc reverse channel) — plus the self-contained doc schema, via sdk.NewMeta.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.181.0001",
		[]sdk.ProvidedCapability{{Class: "command", Word: "settings"}},
		nil)
}

// CliMain is the out-of-process CLI entrypoint (only reached when settings is NOT compiled in).
// Config-subsystem file I/O no longer strictly needs the reverse channel (config.go's
// GetConfigValue/SetConfigValue/ListConfigValues/ResetConfigValue are pure sdk/kit calls for every
// non-credential key), but out-of-process placement is not settings' supported/tested placement —
// this refuses UNCONDITIONALLY, matching the pre-wave-γ behavior exactly, rather than silently
// exposing a partially-working surface out-of-process. The canonical placement is compiled-in
// (Invoke → provider.go), where a real reverse-channel executor is always threaded.
func CliMain(_ []string) int {
	fmt.Fprintln(os.Stderr, "charly: settings requires compiled-in placement (the reverse channel needed by credential-touching ops is unavailable out-of-process)")
	return 1
}
