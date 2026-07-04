// Package adb is the charly plugin serving the `adb`
// Android-Debug-Bridge check verb AND the `deploy:android` SUBSTRATE (F1) — i.e.
// ALL Android device interaction: the `adb:` verb, the `target: android` app-install
// deploy, and the goadb-backed `charly status` device probe (an importable root package +
// its own go.mod). It exists to keep github.com/zach-klippenstein/goadb OUT of
// charly's core go.mod: the host go-builds this binary and serves it OUT-OF-PROCESS
// over go-plugin gRPC via the charly plugin SDK, so the `adb:` verb dispatches through
// the provider registry exactly like a built-in (the verb keeping its `adb:`
// discriminator + every modifier on charly's core #Op, authoring unchanged) AND the
// `target: android` deploy resolves to this plugin's deploy:android provider over the
// E3b reverse channel. One plugin owns the FULL adb/goadb dependency + the single apk
// install path (R3 — no duplicate installer across verb and deploy).
//
// Dual-placement by construction: the SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS
// over go-plugin gRPC when they are not — placement is invisible above the registry.
package adb

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the adb provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:adb AND deploy:android + the plugin's self-contained CUE
// schema (via sdk.NewMeta → BuildCapabilities). Both keep their entire authoring
// contract on charly's core schema — the verb's #AdbMethod enum + modifiers on #Op,
// the deploy substrate's fields on #Android / the apk: format — so neither carries
// plugin_input; the advertised capabilities carry an EMPTY InputDef and the served
// schema (adb.cue) exists only to satisfy the host's non-empty-schema load gate.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.180.0001",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "adb", InputDef: ""},
			{Class: "deploy", Word: "android", InputDef: ""},
		},
		schemaFS)
}
