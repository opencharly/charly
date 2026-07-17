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
// host's registry-coupled callbacks (parse pre-scan, ref resolution, the #NodeDoc gate) — this
// candy never touches the registry directly (boundary law clause D). The repo-identity cycle-break
// (seams.RepoIdentity + the rootIdentity seed) is NOT registry-coupled — it's pure fs/git/yaml
// logic (loaderkit.RepoIdentity/RootRepoIdentity) this candy composes ITSELF when the host leaves
// it unset, so charly core need not hold that logic just to thread a function value through.
func (*provider) WalkProject(rootDir string, rootData []byte, rootIdentity string, seams spec.WalkSeams) (spec.LoadedProject, error) {
	if seams.RepoIdentity == nil {
		seams.RepoIdentity = loaderkit.RepoIdentity
	}
	if rootIdentity == "" {
		rootIdentity = loaderkit.RootRepoIdentity(rootDir)
	}
	return loaderkit.Walk(rootDir, rootData, rootIdentity, seams)
}

// ScanCandyManifest implements spec.CandyScanner — the typed CANDY-SCAN the host calls once per
// candy directory (compiled-in, no wire envelope): fs-probes + manifest parse + the derived-logic
// construction (bake_plugin→require, package-section derivation, port normalization), delegating
// to the ONE copy in sdk/loaderkit. parseManifest is the host-injected, registry-coupled manifest
// parse (mirrors WalkSeams.Parser above) — the candy-manifest parse threads the registered
// DocParser + the registry-derived Threaded snapshot, so it stays host-side; only the scan+construct
// logic moves here.
func (*provider) ScanCandyManifest(path, name, manifestName string, parseManifest func(path string) (*spec.Candy, error)) (spec.CandyModel, spec.CandyView, spec.CandyRefs, error) {
	return loaderkit.ScanCandyManifest(path, name, manifestName, parseManifest)
}

// ScanInlineCandy implements spec.CandyScanner's inline half — a candy declared directly in a
// unified charly.yml (no separate manifest file, ly already parsed), delegating to the same
// sdk/loaderkit construction logic ScanCandyManifest uses.
func (*provider) ScanInlineCandy(name, sourceDir string, ly *spec.Candy) (spec.CandyModel, spec.CandyView, spec.CandyRefs) {
	return loaderkit.ScanInlineCandy(name, sourceDir, ly)
}

// ScanRemoteCandy implements spec.CandyScanner's remote-repo half — scanning specific candies out
// of a downloaded remote repository directory (only the bare refs in wantRefs), delegating to the
// same sdk/loaderkit construction logic ScanCandyManifest uses, plus the Remote/RepoPath/SubPathPrefix
// mutation + sibling-dep qualification loaderkit.ScanRemoteCandy performs.
func (*provider) ScanRemoteCandy(repoDir, repoPath string, wantRefs map[string]bool, parseManifest func(path string) (*spec.Candy, error)) (map[string]spec.ScannedCandy, error) {
	return loaderkit.ScanRemoteCandy(repoDir, repoPath, wantRefs, parseManifest)
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
