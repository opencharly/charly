// Command plugin-vnc is the OUT-OF-TREE charly plugin serving the `vnc` RFB/VNC
// check verb (a standalone Go module, its own go.mod). It drives a live deployment's
// VNC desktop over the RFB protocol — status/screenshot/click/mouse/type/key/rfb —
// speaking RFC 6143 (the custom stdlib-only VNC client: VeNCrypt/TLS + ZRLE decode).
// The host go-builds this binary and serves it OUT-OF-PROCESS over go-plugin gRPC via
// the charly plugin SDK, so the `vnc:` verb dispatches through the provider registry
// exactly like a built-in — with the verb keeping its `vnc:` discriminator + every
// modifier (x/y/text/key/artifact/…) on charly's core #Op (authoring unchanged). The
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

// Describe ships the plugin's capability (verb:vnc) AND its self-contained CUE schema
// over the wire via sdk.BuildCapabilities. vnc keeps its entire authoring contract (the
// #VncMethod enum + every modifier) on charly's core #Op — like cdp/mcp/spice, it has NO
// plugin_input — so the advertised capability carries an EMPTY InputDef and the served
// schema (vnc.cue) exists only to satisfy the host's non-empty-schema load gate. The SDK
// compiles the schema standalone here, failing loudly before serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.178.1200",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "vnc", InputDef: ""}},
		schemaFS, "schema")
}
