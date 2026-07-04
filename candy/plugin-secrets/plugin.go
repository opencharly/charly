// Package secrets is the charly plugin serving the ENTIRE secrets subsystem — it owns the
// credential store (Secret Service / config-file backends) + the GPG `.secrets` surface, so
// github.com/zalando/go-keyring lives HERE, out of charly's core binary entirely (the C2 dep-shed
// removed go-keyring from charly/go.mod). It is an importable dual-placement plugin — the SAME
// NewProvider()/NewMeta()/CliMain compile INTO charly in-process when listed in compiled_plugins,
// or cmd/serve serves them OUT-OF-PROCESS when they are not. It provides TWO capabilities:
//
//   - verb:credential — the externalized CREDENTIAL STORE BACKEND (NOT a check verb).
//     charly's core pluginCredentialStore (charly/credential_plugin.go) forwards every
//     CredentialStore method (get/set/delete/list/name), the env-less resolve
//     (resolve → {value,source}), the doctor keyring health probe (health), and the
//     keyring re-probe (reset) over go-plugin gRPC. The host go-builds this binary and
//     serves it OUT-OF-PROCESS via LocalTransport, so the keyring backend dispatches
//     through the provider registry exactly like a built-in — every core credential
//     consumer (enc.go / secrets.go / layer_secrets.go / config_secret_migration.go /
//     runtime_config.go / vnc_preresolve.go) is unchanged.
//
//   - command:secrets — `charly secrets …`, the externalized secrets CLI (list / get /
//     set / delete / import / export / migrate-secrets + the `gpg` subgroup). Dispatched
//     by charly syscall.Exec'ing this binary in CLI mode (sdk.Main → cliMain, command.go),
//     so it owns real terminal stdio/TTY: secure password prompts (term.ReadPassword),
//     $EDITOR for `secrets gpg edit`, and live `gpg` shell-outs all work natively.
//
// verb:credential is served over gRPC (the provider registry); command:secrets is
// served via the CLI syscall.Exec path — so command:secrets is declared in
// plugin.providers (for the CLI-grammar prescan + baked manifest) but NOT advertised
// in Describe (mirrors candy/plugin-mcp's command:mcp).
package secrets

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the credential-store provider (the Invoke dispatch surface).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:credential (the externalized credential store backend) + the
// plugin's self-contained CUE schema (via sdk.NewMeta → BuildCapabilities). verb:credential
// carries no AUTHORED plugin_input (its params are the internal CredentialInput RPC the core
// adapter sends, never a plan step), so it advertises an EMPTY InputDef and the served schema
// (schema/credential.cue) exists only to satisfy the host's non-empty-schema load gate
// (mirrors plugin-mcp).
//
// command:secrets (`charly secrets …`, the externalized CLI) is NOT advertised here: it is
// dispatched by charly syscall.Exec'ing this binary in CLI mode (cliMain), not resolved
// through the gRPC provider registry — so it carries no Describe capability. The candy's
// plugin.providers declaration still lists command:secrets (CLI-grammar prescan + baked
// `.providers` manifest).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.178.2100",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "credential", InputDef: ""},
		},
		schemaFS)
}

// CliMain is the plugin's CLI entrypoint (command:secrets dispatch — `charly secrets …`).
func CliMain(args []string) int { return cliMain(args) }
