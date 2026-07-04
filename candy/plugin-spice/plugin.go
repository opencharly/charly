// Package spice is the charly plugin serving the `spice`
// SPICE-wire display/input check verb (an importable root package + its own go.mod). It
// exists to keep github.com/Shells-com/spice — and its cgo audio transitives
// github.com/hraban/opus + github.com/gordonklaus/portaudio — OUT of charly's core
// go.mod: the host go-builds this binary and serves it OUT-OF-PROCESS over go-plugin
// gRPC via the charly plugin SDK, so the `spice:` verb dispatches through the
// provider registry exactly like a built-in — with the verb keeping its `spice:`
// discriminator + every modifier on charly's core #Op (authoring unchanged). The
// fourth external dep-shed (after candy/plugin-appium, candy/plugin-adb,
// candy/plugin-kube); the Shells-com/spice library lives HERE (vendored under
// third_party/spice), with its cgo opus/portaudio audio channels removed entirely so
// it is pure Go — no opus/portaudio dependency, no cgo, no build tag.
//
// The plugin DIALS a pre-resolved SPICE endpoint (host:port or UNIX socket) the host
// hands it via the check env — the host owns the go-libvirt VM resolution
// (vm_target.go's ResolveVmTarget + SpiceEndpoint) and any qemu+ssh:// side tunnel,
// so this module needs no libvirt at all.
//
// Dual-placement by construction: the SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS
// over go-plugin gRPC when they are not — placement is invisible above the registry.
package spice

import (
	"context"
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the spice provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the plugin's capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the plugin's capability (verb:spice) AND its self-contained CUE
// schema over the wire via sdk.BuildCapabilities. spice keeps its entire authoring
// contract (the #SpiceMethod enum + every modifier) on charly's core #Op — like
// cdp/vnc, it has NO plugin_input — so the advertised capability carries an EMPTY
// InputDef and the served schema (spice.cue) exists only to satisfy the host's
// non-empty-schema load gate. The SDK compiles the schema standalone here, failing
// loudly before serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.174.1700",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "spice", InputDef: ""}},
		schemaFS, "schema")
}
