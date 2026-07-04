// Command plugin-adb is the OUT-OF-TREE charly plugin serving the `adb`
// Android-Debug-Bridge check verb AND the `deploy:android` SUBSTRATE (F1) — i.e.
// ALL Android device interaction: the `adb:` verb, the `target: android` app-install
// deploy, and the goadb-backed `charly status` device probe (a standalone Go module,
// its own go.mod). It exists to keep github.com/zach-klippenstein/goadb OUT of
// charly's core go.mod: the host go-builds this binary and serves it OUT-OF-PROCESS
// over go-plugin gRPC via the charly plugin SDK, so the `adb:` verb dispatches through
// the provider registry exactly like a built-in (the verb keeping its `adb:`
// discriminator + every modifier on charly's core #Op, authoring unchanged) AND the
// `target: android` deploy resolves to this plugin's deploy:android provider over the
// E3b reverse channel. One plugin owns the FULL adb/goadb dependency + the single apk
// install path (R3 — no duplicate installer across verb and deploy).
package main

import (
	"context"
	"embed"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

func main() { sdk.Serve(&provider{}, &meta{}) }

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the plugin's capabilities (verb:adb AND deploy:android) plus its
// self-contained CUE schema over the wire via sdk.BuildCapabilities. Both keep their
// entire authoring contract on charly's core schema — the verb's #AdbMethod enum +
// modifiers on #Op, the deploy substrate's fields on #Android / the apk: format — so
// neither carries plugin_input; the advertised capabilities carry an EMPTY InputDef
// and the served schema (adb.cue) exists only to satisfy the host's non-empty-schema
// load gate. The SDK compiles the schema standalone here, failing loudly before
// serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.180.0001",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "adb", InputDef: ""},
			{Class: "deploy", Word: "android", InputDef: ""},
		},
		schemaFS, "schema")
}
