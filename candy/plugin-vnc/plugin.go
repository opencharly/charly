// Package vnc is the charly plugin serving the `vnc` RFB/VNC
// check verb (an importable root package + its own go.mod). It drives a live deployment's
// VNC desktop over the RFB protocol — status/screenshot/click/mouse/type/key/rfb —
// speaking RFC 6143 (the custom stdlib-only VNC client: VeNCrypt/TLS + ZRLE decode).
// The host go-builds this binary and serves it OUT-OF-PROCESS over go-plugin gRPC via
// the charly plugin SDK, so the `vnc:` verb dispatches through the provider registry
// exactly like a built-in. Since the schema-compaction cutover an authored `vnc:` step
// desugars to the internal plugin/plugin_input envelope, and every vnc-exclusive
// modifier (method/x/y/text/key/artifact/…) lives in the plugin's OWN #VncInput
// (schema/vnc.cue → the generated params.VncInput). The
// latest external dep-shed after candy/plugin-cdp; the RFB client lives HERE now, out
// of charly's core check surface (nothing remains in-core — the VM-VNC CLI subsumed
// into the declarative `vnc:` verb against a vm target).
//
// The plugin owns NO podman / venue / libvirt / port-mapping machinery — the host
// pre-resolves the deployment's VNC endpoint (preresolveVncEndpoint, charly/
// vnc_preresolve.go): a container's published port 5900, OR a VM's libvirt-discovered
// <graphics type='vnc'> listener bridged/tunneled to a host-reachable TCP address —
// and hands it over (plus the resolved password) via the check env, so this module
// just dials a plain "host:port" and needs no venue resolution at all.
//
// Dual-placement by construction: the SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS
// over go-plugin gRPC when they are not — placement is invisible above the registry.
package vnc

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewProvider returns the vnc provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises verb:vnc + the plugin's self-contained CUE schema (via
// sdk.NewMeta → BuildCapabilities). The verb's entire authoring contract — the
// method enum + every vnc-exclusive modifier — lives in the served #VncInput
// (schema/vnc.cue), which the host splices onto the base and validates every
// authored `vnc:` step's plugin_input against.
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.178.1200",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "vnc", InputDef: "#VncInput"}},
		schemaFS)
}
