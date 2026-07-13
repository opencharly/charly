// Package loader is the loader plugin candy — the swappable config front-end (P6). It serves the
// per-document PARSE via loaderkit.DocParser: the host resolves the registered loader provider to
// a loaderkit.DocParser and calls it for every config document, so an alternative loader plugin
// can serve a different config front-end by registering a different DocParser. The DEFAULT
// (this candy) parses charly's node-form via the shared sdk/loaderkit — the ONE copy of the parse
// (R3), the way sdk/kit is the one copy of the check walk.
//
// The parse consults ONLY spec vocabulary + yaml + the host-threaded loaderkit.Threaded
// (registry-derived kind-recognition DATA), never charly core — a compiled-in plugin candy is a
// separate module importing only sdk. The bootstrap SEED (the embedded providers: manifest via a
// plain yaml.Unmarshal) STAYS in core and never calls the loader, so registering this at init()
// before the first load has NO bootstrap cycle (RDD-proven).
//
// PLACEMENT — COMPILED-IN (in the embedded compiled_plugins:): the loader must ALWAYS resolve,
// it IS the config front-end every command reaches. Registered at init() before the first load;
// the host calls its typed ParseDoc (no wire envelope) per document.
package loader

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

const calver = "2026.192.0000"

// NewProvider returns the loader provider — a pb.ProviderServer that ALSO implements
// loaderkit.DocParser (the typed per-document parse the host calls compiled-in).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the loader capability (Class "loader", word "loader").
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver, []sdk.ProvidedCapability{
		{Class: "loader", Word: "loader"},
	}, nil)
}

type provider struct{ pb.UnimplementedProviderServer }

// ParseDoc implements spec.DocParser — the typed per-document parse the host calls for every
// config document (compiled-in, no wire envelope). The default charly node-form parse, delegating
// to the ONE copy in sdk/loaderkit.
func (*provider) ParseDoc(doc *yaml.Node, t spec.Threaded) (map[string]*yaml.Node, spec.ParsedProject, error) {
	return loaderkit.ParseDoc(doc, t)
}

// Invoke serves the out-of-process placement. The compiled-in placement uses the typed ParseDoc
// above; the wire OpLoad path (carrying the document + threaded data as JSON) lands with
// out-of-process loader support.
func (*provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetOp() {
	case sdk.OpLoad:
		return nil, fmt.Errorf("loader: out-of-process OpLoad not yet wired — the compiled-in loader uses the typed DocParser path")
	default:
		return nil, fmt.Errorf("loader: unsupported op %q", req.GetOp())
	}
}
