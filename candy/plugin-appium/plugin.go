// Package appium is the charly plugin serving the `appium`
// W3C-WebDriver check verb (an importable root package + its own go.mod). It exists to keep
// github.com/tebeka/selenium (and its ~80 transitive packages) OUT of charly's core
// go.mod: the host go-builds this binary and serves it OUT-OF-PROCESS over go-plugin
// gRPC via the charly plugin SDK, so the `appium:` verb dispatches through the provider
// registry exactly like a built-in — with the verb keeping its `appium:` discriminator
// + every modifier on charly's core #Op (authoring unchanged). The first external
// dep-shed; establishes the external-plugin loading pattern.
//
// Dual-placement by construction: the SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS
// over go-plugin gRPC when they are not — placement is invisible above the registry.
package appium

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the appium provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:appium + the plugin's self-contained CUE schema (via
// sdk.NewMeta → BuildCapabilities). appium keeps its entire authoring contract (the
// #AppiumMethod enum + every modifier) on charly's core #Op — like cdp/vnc it has NO
// plugin_input — so the advertised capability carries an EMPTY InputDef and the served
// schema (appium.cue) exists only to satisfy the host's non-empty-schema load gate.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.174.0700",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "appium", InputDef: ""}},
		schemaFS)
}
