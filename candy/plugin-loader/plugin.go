// Package loader is the loader plugin candy — the swappable config front-end (P6) AND, since #46,
// the swappable whole-project WALK. It serves two typed (no wire envelope) seams:
//
//   - spec.DocParser — the per-document PARSE: the host resolves the registered loader provider
//     to a spec.DocParser and calls it for every config document.
//   - spec.ProjectWalker — the whole-project WALK (import queue + discover + namespaced-import
//     mounts): the host resolves the registered loader provider to a spec.ProjectWalker and calls
//     it once per project load, passing a spec.WalkSeams built from host callbacks.
//
// Both delegate to the shared sdk/loaderkit (loaderkit.ParseDoc / loaderkit.Walk) — the ONE copy
// of the parse+walk mechanism (R3), the way sdk/kit is the one copy of the check walk. An
// alternative loader plugin serves a different config front-end / walk mechanism by implementing
// the same two interfaces.
//
// The parse+walk consult ONLY spec vocabulary + yaml + the host-threaded spec.Threaded
// (registry-derived kind-recognition DATA) + the host-supplied spec.WalkSeams callbacks, never
// charly core directly — a compiled-in plugin candy is a separate module importing only sdk. The
// bootstrap SEED (the embedded providers: manifest via a plain yaml.Unmarshal) STAYS in core and
// never calls the loader, so registering this at init() before the first load has NO bootstrap
// cycle (RDD-proven).
//
// PLACEMENT — COMPILED-IN (in the embedded compiled_plugins:): the loader must ALWAYS resolve,
// it IS the config front-end every command reaches. Registered at init() before the first load;
// the host calls its typed ParseDoc / WalkProject (no wire envelope) directly.
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
// spec.DocParser (the typed per-document parse the host calls compiled-in).
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

// WalkProject implements spec.ProjectWalker — the typed whole-project WALK the host calls once
// per project load (compiled-in, no wire envelope): import queue + discover + namespaced-import
// mounts + per-document parse, delegating to the ONE copy in sdk/loaderkit. seams carries the
// host's registry-coupled callbacks (parse pre-scan, ref resolution, the #NodeDoc gate, the
// repo-identity cycle-break) — this candy never touches the registry directly (boundary law
// clause D).
func (*provider) WalkProject(rootDir string, rootData []byte, rootIdentity string, seams spec.WalkSeams) (spec.LoadedProject, error) {
	return loaderkit.Walk(rootDir, rootData, rootIdentity, seams)
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
