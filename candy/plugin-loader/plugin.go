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
	"encoding/json"
	"fmt"
	"reflect"

	"cuelang.org/go/cue"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
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

// MaterializeNode implements spec.Materializer — the typed per-node kind-decode DISPATCH policy
// the host calls once per parsed entity node (compiled-in, no wire envelope): the NOT-FOUND
// fallback (route to the bundle builder / defer-during-connect-pass / warn-and-skip / hard error)
// delegating to the ONE copy in sdk/loaderkit. The actual registry resolve + provider dispatch
// stays host-side, reached through seams.DecodeEntity/BuildBundleEntity (boundary law clause M —
// this candy never touches the registry directly). K1 unit 1.
func (*provider) MaterializeNode(pn spec.ParsedNode, t spec.Threaded, seams spec.MaterializeSeams, acc *spec.MaterializedProject) error {
	return loaderkit.Materialize(pn, t, seams, acc)
}

// LoadUnified implements spec.ProjectLoader — the typed whole-project LOAD-ENTRY the host calls to
// load a project's charly.yml (compiled-in, no wire envelope): it drives the ONE copy of the
// kind-blind load orchestration in sdk/loaderkit (loaderkit.LoadUnified) over a LoadSeams built from
// the host-supplied registry-/host-coupled legs (exec, a spec.LoaderExecutor). So charly core reaches
// the loader mechanism ONLY through this spec-typed seam — it never imports loaderkit to load its own
// config (#55 loader-keystone). An alternative loader plugin serves a different whole-project load by
// implementing this same interface.
func (*provider) LoadUnified(dir string, exec spec.LoaderExecutor) (*spec.UnifiedFile, bool, error) {
	return loaderkit.LoadUnified(dir, loaderkit.LoadSeamsFromExecutor(exec))
}

// DecodeEntityViaCUE implements spec.ProjectLoader — the typed per-entity CUE decode the host calls
// for every kind/candy/node-form decode (compiled-in, no wire envelope): delegates to the ONE copy
// of the relocated shorthand-normalize + CUE-ingest + Decode mechanism in sdk/loaderkit (K1 unit 1).
func (*provider) DecodeEntityViaCUE(node *yaml.Node, t reflect.Type, out any, label string) error {
	return loaderkit.DecodeEntityViaCUE(node, t, out, label)
}

// ValidateEntityClosedCUE implements spec.ProjectLoader — the typed closed-schema entity check the
// host calls (compiled-in, no wire envelope): delegates to the ONE copy of the relocated
// CUE-validate mechanism in sdk/loaderkit (K1 unit 2).
func (*provider) ValidateEntityClosedCUE(kind, label string, entity cue.Value) error {
	return loaderkit.ValidateEntityClosedCUE(kind, label, entity)
}

// ValidateEntityCUE implements spec.ProjectLoader — the CONCRETE entity check (closedness plus
// missing-required / unresolved disjunctions), delegating to the ONE copy in sdk/loaderkit.
func (*provider) ValidateEntityCUE(kind, label string, entity cue.Value) error {
	return loaderkit.ValidateEntityCUE(kind, label, entity)
}

// CueDocFromYAML implements spec.ProjectLoader — the typed YAML→cue.Value ingest the host calls
// (compiled-in, no wire envelope): delegates to the ONE copy in sdk/loaderkit (K1 unit 2).
func (*provider) CueDocFromYAML(path string, data []byte) (cue.Value, error) {
	return loaderkit.CueDocFromYAML(path, data)
}

// ValidateNodeDocCUE implements spec.ProjectLoader — the typed load-time #NodeDoc structural gate
// the host calls (compiled-in, no wire envelope): delegates to the ONE copy in sdk/loaderkit (K1
// unit 2).
func (*provider) ValidateNodeDocCUE(label string, data []byte) error {
	return loaderkit.ValidateNodeDocCUE(label, data)
}

// ApplyCueDefaults implements spec.ProjectLoader — the typed post-merge schema-defaults fill the
// host calls (compiled-in, no wire envelope): delegates to the ONE copy in sdk/loaderkit (K1 unit
// 2).
func (*provider) ApplyCueDefaults(kind string, out any) error {
	return loaderkit.ApplyCueDefaults(kind, out)
}

// IsResourceDisc / BundleTargetForDisc / SetBundleCrossRef / IsStandaloneResourceKind /
// FoldStandaloneTemplateReply implement spec.ProjectLoader — the typed bundle/resource-member
// kind-decode SUPPORT helpers the host calls (compiled-in, no wire envelope): delegate to the ONE
// copy in sdk/loaderkit (K1 unit 3a).
func (*provider) IsResourceDisc(d string, t spec.Threaded) bool {
	return loaderkit.IsResourceDisc(d, t)
}

func (*provider) BundleTargetForDisc(d string, t spec.Threaded) string {
	return loaderkit.BundleTargetForDisc(d, t)
}

func (*provider) SetBundleCrossRef(dn *spec.BundleNode, disc, ref string, t spec.Threaded) {
	loaderkit.SetBundleCrossRef(dn, disc, ref, t)
}

func (*provider) IsStandaloneResourceKind(disc string, t spec.Threaded) bool {
	return loaderkit.IsStandaloneResourceKind(disc, t)
}

func (*provider) FoldStandaloneTemplateReply(disc, name string, replyJSON json.RawMessage, acc *spec.MaterializedProject) error {
	return loaderkit.FoldStandaloneTemplateReply(disc, name, replyJSON, acc)
}

// AssembleEntityBody / DecodeNodeValue / EntityBodyJSON / BuildBundleNode /
// BuildResourceMemberChildren / BuildBundleNodeInto / IsDeployShape / DecodeStandaloneTemplateJSON /
// ResourceChildren implement spec.ProjectLoader — the typed entity-body assembly +
// bundle/resource-member tree-builder mechanism the host calls (compiled-in, no wire envelope):
// delegate to the ONE copy in sdk/loaderkit (K1 unit 3b).
func (*provider) AssembleEntityBody(pn spec.ParsedNode) (*yaml.Node, error) {
	return loaderkit.AssembleEntityBody(pn)
}

func (*provider) DecodeNodeValue(pn spec.ParsedNode, out any) error {
	return loaderkit.DecodeNodeValue(pn, out)
}

func (*provider) EntityBodyJSON(pn spec.ParsedNode) (json.RawMessage, error) {
	return loaderkit.EntityBodyJSON(pn)
}

func (*provider) BuildBundleNode(pn spec.ParsedNode, t spec.Threaded) (*spec.BundleNode, error) {
	return loaderkit.BuildBundleNode(pn, t)
}

func (*provider) BuildResourceMemberChildren(pn spec.ParsedNode, t spec.Threaded) (map[string]*spec.BundleNode, error) {
	return loaderkit.BuildResourceMemberChildren(pn, t)
}

func (*provider) BuildBundleNodeInto(pn spec.ParsedNode, t spec.Threaded, acc *spec.MaterializedProject) error {
	return loaderkit.BuildBundleNodeInto(pn, t, acc)
}

func (*provider) IsDeployShape(pn spec.ParsedNode) bool {
	return loaderkit.IsDeployShape(pn)
}

func (*provider) DecodeStandaloneTemplateJSON(pn spec.ParsedNode, t spec.Threaded) (json.RawMessage, error) {
	return loaderkit.DecodeStandaloneTemplateJSON(pn, t)
}

func (*provider) ResourceChildren(pn spec.ParsedNode) []spec.ParsedNode {
	return loaderkit.ResourceChildren(pn)
}

// ValidateCandyManifestCUE / ValidateNodeFormSteps implement spec.ProjectLoader — the typed
// box-validate entity-tree walk the host calls (compiled-in, no wire envelope): delegate to the
// ONE copy in sdk/loaderkit (K1 unit 3c).
func (*provider) ValidateCandyManifestCUE(path string, data []byte, t spec.Threaded, parser spec.DocParser) error {
	return loaderkit.ValidateCandyManifestCUE(path, data, t, parser)
}

func (*provider) ValidateNodeFormSteps(path string, data []byte, t spec.Threaded, parser spec.DocParser) error {
	return loaderkit.ValidateNodeFormSteps(path, data, t, parser)
}

// ResolveMergedDeployTree implements spec.ProjectLoader — the merged project+overlay deploy-node
// tree read the host's check seams need (compiled-in, no wire envelope): it drives the ONE copy
// of the loaderkit project+per-host-overlay projection+merge (loaderkit.ResolveMergedTreeViaExecutor)
// over the in-proc executor the host threaded on ctx (sdk.ContextWithExecutor →
// sdk.ExecutorFromContext — the SAME in-proc reverse-channel path ExecutorForInvoke uses for
// Invoke). So charly core reaches the merged-tree read ONLY through this spec-typed seam — it
// never imports loaderkit for it (#55 coneA Q2(1), check_cmd.go sheds its loaderkit import).
func (*provider) ResolveMergedDeployTree(ctx context.Context, dir string) (map[string]spec.BundleNode, error) {
	ex, ok := sdk.ExecutorFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("resolve merged deploy tree: no host reverse channel on context (command not compiled-in?)")
	}
	return loaderkit.ResolveMergedTreeViaExecutor(ctx, ex, dir)
}

// MaterializeLoadedProject / MarshalMaterialized / ValidateAndroidDevices / ValidatePreemptible
// implement the whole-project loader ops on spec.ProjectLoader (#55 2b C3): charly core reaches the
// loaderkit materialize/validate MECHANISM through this compiled-in seam (no wire envelope) instead of
// importing loaderkit itself. The orchestration/validation LOGIC stays in the ONE copy in sdk/loaderkit;
// the host supplies the registry-/host-coupled seams + resolve callbacks.
func (*provider) MaterializeLoadedProject(lp *spec.LoadedProject, merged *spec.UnifiedFile, byID map[int64]*spec.UnifiedFile, seams spec.MaterializeProjectSeams) error {
	return loaderkit.MaterializeLoadedProject(lp, merged, byID, seams)
}

func (*provider) MarshalMaterialized(uf *spec.UnifiedFile) ([]byte, error) {
	return loaderkit.MarshalMaterialized(uf)
}

func (*provider) ValidateAndroidDevices(uf *spec.UnifiedFile, resolveAndroid func(json.RawMessage) (*spec.ResolvedAndroid, error)) error {
	return loaderkit.ValidateAndroidDevices(uf, resolveAndroid)
}

func (*provider) ValidatePreemptible(uf *spec.UnifiedFile, resolveResource func(json.RawMessage) (*spec.ResolvedResource, error), resolveVm func(json.RawMessage) (*spec.ResolvedVm, error)) error {
	return loaderkit.ValidatePreemptible(uf, resolveResource, resolveVm)
}

// ScanCandyFromLocal / RunDiscover / FinalizeScannedCandies implement the candy-scan + discover ops on
// spec.ProjectLoader (#55 C3b-ii): charly core reaches the loaderkit scan/discover MECHANISM through
// this compiled-in seam (no wire envelope) instead of importing loaderkit. The fix-point / discover /
// finalize LOGIC stays in the ONE copy in sdk/loaderkit; the host supplies the ScanSeams / WalkSeams
// host-coupled closures.
func (*provider) ScanCandyFromLocal(localScanned map[string]spec.ScannedCandy, initCfg *spec.InitConfig, seams spec.ScanSeams) (map[string]spec.CandyReader, error) {
	return loaderkit.ScanCandyFromLocal(localScanned, initCfg, seams)
}

func (*provider) RunDiscover(rootDir string, specs []spec.ScanSpec, seams spec.WalkSeams) ([]spec.DiscoveredManifest, error) {
	return loaderkit.RunDiscover(rootDir, specs, seams)
}

func (*provider) FinalizeScannedCandies(scanned map[string]spec.ScannedCandy, initCfg *spec.InitConfig) map[string]spec.CandyReader {
	return loaderkit.FinalizeScannedCandies(scanned, initCfg)
}

// EnsureRepoDownloaded / CollectRemoteRefsOpts implement spec.ProjectLoader — the typed remote-repo
// fetch orchestration + candy-ref collection mechanism the host calls (compiled-in, no wire
// envelope): delegate to the ONE copy in sdk/loaderkit (K1 unit 4). Since K-wave 2 cone R1 the
// host-coupled legs are built HERE (refs_seams.go) off the ctx-threaded executor rather than handed
// in by charly core — see that file for why core assembling them was an R-item, not a mechanism.
func (*provider) EnsureRepoDownloaded(ctx context.Context, repoPath, version string) (string, error) {
	seams, err := refsSeams(ctx)
	if err != nil {
		return "", err
	}
	return loaderkit.EnsureRepoDownloaded(repoPath, version, seams)
}

func (*provider) CollectRemoteRefsOpts(ctx context.Context, cfg *spec.Config, layers map[string]spec.CandyReader, opts spec.ResolveOpts) ([]spec.RemoteDownload, error) {
	seams, err := refsSeams(ctx)
	if err != nil {
		return nil, err
	}
	return loaderkit.CollectRemoteRefsOpts(cfg, layers, opts, seams)
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
