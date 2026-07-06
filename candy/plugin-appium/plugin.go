// Package appium is the charly plugin serving the `appium`
// W3C-WebDriver check verb (an importable root package + its own go.mod). It exists to keep
// github.com/tebeka/selenium (and its ~80 transitive packages) OUT of charly's core
// go.mod: the host go-builds this binary and serves it OUT-OF-PROCESS over go-plugin
// gRPC via the charly plugin SDK, so the `appium:` verb dispatches through the provider
// registry exactly like a built-in — the authored `appium: <method>` sugar desugars to
// plugin/plugin_input; the method + per-verb fields ride the input map, validated
// against this plugin's own #AppiumInput. The first external dep-shed; establishes the
// external-plugin loading pattern.
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
// sdk.NewMeta → BuildCapabilities). The verb's plugin_input validates against the
// served #AppiumInput (the method enum + every appium-exclusive modifier moved here
// from core #Op in the schema-compaction cutover).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.174.0700",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "appium", InputDef: "#AppiumInput", Primary: "method"}},
		schemaFS)
}
