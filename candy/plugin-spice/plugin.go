// Package spice is the charly plugin serving the `spice`
// SPICE-wire display/input check verb (an importable root package + its own go.mod). It
// exists to keep github.com/Shells-com/spice — and its cgo audio transitives
// github.com/hraban/opus + github.com/gordonklaus/portaudio — OUT of charly's core
// go.mod: the host go-builds this binary and serves it OUT-OF-PROCESS over go-plugin
// gRPC via the charly plugin SDK, so the `spice:` verb dispatches through the
// provider registry exactly like a built-in. Since the schema-compaction cutover an
// authored `spice:` step desugars to the internal plugin/plugin_input envelope, and
// every spice-exclusive modifier (method/x/y/text/key/artifact/…) lives in the
// plugin's OWN #SpiceInput (schema/spice.cue → the generated params.SpiceInput). The
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
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the spice provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:spice + the plugin's self-contained CUE schema (via
// sdk.NewMeta → BuildCapabilities). The verb's entire authoring contract — the
// method enum + every spice-exclusive modifier — lives in the served #SpiceInput
// (schema/spice.cue), which the host splices onto the base and validates every
// authored `spice:` step's plugin_input against.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.174.1700",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "spice", InputDef: "#SpiceInput"}},
		schemaFS)
}
