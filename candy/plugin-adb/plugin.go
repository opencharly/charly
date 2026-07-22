// Package adb is the charly plugin serving the `adb`
// Android-Debug-Bridge check verb AND the `deploy:android` SUBSTRATE (F1) — i.e.
// ALL Android device interaction: the `adb:` verb, the `target: android` app-install
// deploy, and the goadb-backed `charly status` device probe (an importable root package +
// its own go.mod). It exists to keep github.com/zach-klippenstein/goadb OUT of
// charly's core go.mod: the host go-builds this binary and serves it OUT-OF-PROCESS
// over go-plugin gRPC via the charly plugin SDK, so the `adb:` verb dispatches through
// the provider registry exactly like a built-in (the authored `adb: <method>` sugar
// desugars to plugin/plugin_input; the method + per-verb fields ride the input map,
// validated against this plugin's own #AdbInput) AND the `target: android` deploy
// resolves to this plugin's deploy:android provider over the E3b reverse channel. One
// plugin owns the FULL adb/goadb dependency + the single apk install path (R3 — no
// duplicate installer across verb and deploy).
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
// schema (via sdk.NewMeta → BuildCapabilities). The verb's plugin_input validates
// against the served #AdbInput (the method enum + every adb-exclusive modifier moved
// here from core #Op in the schema-compaction cutover); the deploy substrate keeps
// its authoring contract on core #Android / the apk: format and carries an EMPTY
// InputDef. Preresolve:true (F6, FINAL/K5 unit 6a) declares the wire-backed
// OpPreresolve leg (preresolve.go) — candy/plugin-bundle's preresolveSubstrate
// (S3b — was the core-side deploy_preresolve.go:wireDeployPreresolver registry
// before the deploy-dispatch cluster moved) dispatches directly to THIS plugin via
// sdk.Executor.InvokeProvider, reaching what was the deleted charly/android_deploy_preresolve.go
// body.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.180.0001",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "adb", InputDef: "#AdbInput", Primary: "method"},
			{Class: "deploy", Word: "android", InputDef: "", Preresolve: true},
		},
		schemaFS)
}
