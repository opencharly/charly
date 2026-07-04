// Command plugin-appium is the OUT-OF-TREE charly plugin serving the `appium`
// W3C-WebDriver check verb (a standalone Go module, its own go.mod). It exists to keep
// github.com/tebeka/selenium (and its ~80 transitive packages) OUT of charly's core
// go.mod: the host go-builds this binary and serves it OUT-OF-PROCESS over go-plugin
// gRPC via the charly plugin SDK, so the `appium:` verb dispatches through the provider
// registry exactly like a built-in — with the verb keeping its `appium:` discriminator
// + every modifier on charly's core #Op (authoring unchanged). The first external
// dep-shed; establishes the external-plugin loading pattern.
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

// Describe ships the plugin's capability (verb:appium) AND its self-contained CUE schema
// over the wire via sdk.BuildCapabilities. appium keeps its entire authoring contract
// (the #AppiumMethod enum + every modifier) on charly's core #Op — like cdp/vnc, it has
// NO plugin_input — so the advertised capability carries an EMPTY InputDef and the served
// schema (appium.cue) exists only to satisfy the host's non-empty-schema load gate. The
// SDK compiles the schema standalone here, failing loudly before serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.174.0700",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "appium", InputDef: ""}},
		schemaFS, "schema")
}
