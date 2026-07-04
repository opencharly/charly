// Command plugin-secrets is the OUT-OF-TREE charly plugin serving the ENTIRE secrets
// subsystem — a standalone Go module (its own go.mod) that owns the credential store
// (Secret Service / config-file backends) + the GPG `.secrets` surface, so
// github.com/zalando/go-keyring lives HERE, out of charly's core binary entirely (the
// C2 dep-shed removed go-keyring from charly/go.mod). It provides TWO capabilities:
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
package main

import (
	"context"
	"embed"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// main is dual-mode (sdk.Main): when charly launches this binary over go-plugin gRPC
// (the handshake cookie is set) it SERVES verb:credential; otherwise charly
// syscall.Exec'd it as a command passthrough and it runs the `charly secrets …` CLI
// (cliMain, command.go) with real terminal stdio.
func main() { sdk.Main(&provider{}, &meta{}, cliMain) }

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the plugin's ONE gRPC-served capability — verb:credential (the
// externalized credential store backend) — plus the self-contained CUE schema, over
// the wire via sdk.BuildCapabilities. verb:credential carries no AUTHORED plugin_input
// (its params are the internal CredentialInput RPC the core adapter sends, never a plan
// step), so it advertises an EMPTY InputDef and the served schema (schema/credential.cue)
// exists only to satisfy the host's non-empty-schema load gate (mirrors plugin-mcp).
//
// command:secrets (`charly secrets …`, the externalized CLI) is NOT advertised here: it
// is dispatched by charly syscall.Exec'ing this binary in CLI mode (cliMain), not resolved
// through the gRPC provider registry — so it carries no Describe capability. The candy's
// plugin.providers declaration still lists command:secrets (CLI-grammar prescan + baked
// `.providers` manifest).
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.178.2100",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "credential", InputDef: ""},
		},
		schemaFS, "schema")
}
