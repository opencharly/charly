package build

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// resolve_legs.go — the plugin-side leg helpers the build-engine RESOLVE (resolve.go, U6) reaches the
// host for. Each is a thin HostBuild / InvokeProvider wrapper over a charly `buildengine-*` host leg
// (charly/host_build_buildengine.go) or a compiled-in peer plugin. The pattern mirrors the K1 loader
// witness legs (candy/plugin-bundle) — only the genuinely host-coupled steps cross the wire.

// hostBuildJSON marshals req, dispatches HostBuild(kind), and decodes the reply into *Reply. A void
// leg passes a nil *Reply (out ignored).
func hostBuildJSON[Req any](ctx context.Context, ex *sdk.Executor, kind string, req Req, out any) error {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("%s: marshal request: %w", kind, err)
	}
	replyJSON, err := ex.HostBuild(ctx, kind, reqJSON)
	if err != nil {
		return fmt.Errorf("%s: %w", kind, err)
	}
	if out != nil && len(replyJSON) > 0 {
		if err := json.Unmarshal(replyJSON, out); err != nil {
			return fmt.Errorf("%s: decode reply: %w", kind, err)
		}
	}
	return nil
}

// hostVoidLeg dispatches a HostBuild leg that returns no data (the reply is empty/error-only).
func hostVoidLeg[Req any](ctx context.Context, ex *sdk.Executor, kind string, req Req) error {
	return hostBuildJSON(ctx, ex, kind, req, nil)
}

// --- vocab resolve callbacks (plugin↔plugin InvokeProvider over the kind:distro/init OpResolve legs) ---

func resolveDistroLeg(ctx context.Context, ex *sdk.Executor) func(json.RawMessage) (*spec.ResolvedDistro, error) {
	return func(body json.RawMessage) (*spec.ResolvedDistro, error) {
		params, err := json.Marshal(spec.DistroResolveInput{Distro: body})
		if err != nil {
			return nil, err
		}
		res, err := ex.InvokeProvider(ctx, "kind", "distro", sdk.OpResolve, params, nil, sdk.InvokeProviderOpts{})
		if err != nil {
			return nil, err
		}
		var reply spec.DistroResolveReply
		if len(res) > 0 {
			if err := json.Unmarshal(res, &reply); err != nil {
				return nil, fmt.Errorf("distro resolve: decode reply: %w", err)
			}
		}
		return reply.Resolved, nil
	}
}

func resolveInitLeg(ctx context.Context, ex *sdk.Executor) func(json.RawMessage) (*spec.ResolvedInit, error) {
	return func(body json.RawMessage) (*spec.ResolvedInit, error) {
		params, err := json.Marshal(spec.InitResolveRequest{Config: &spec.InitResolveInput{Init: body}})
		if err != nil {
			return nil, err
		}
		res, err := ex.InvokeProvider(ctx, "kind", "init", sdk.OpResolve, params, nil, sdk.InvokeProviderOpts{})
		if err != nil {
			return nil, err
		}
		var reply spec.InitResolveReply
		if len(res) > 0 {
			if err := json.Unmarshal(res, &reply); err != nil {
				return nil, fmt.Errorf("init resolve config: decode reply: %w", err)
			}
		}
		return reply.Resolved, nil
	}
}

func resolveResourceLeg(ctx context.Context, ex *sdk.Executor) func(json.RawMessage) (*spec.ResolvedResource, error) {
	return func(body json.RawMessage) (*spec.ResolvedResource, error) {
		params, err := json.Marshal(spec.ResourceResolveInput{Resource: body})
		if err != nil {
			return nil, err
		}
		res, err := ex.InvokeProvider(ctx, "kind", "resource", sdk.OpResolve, params, nil, sdk.InvokeProviderOpts{})
		if err != nil {
			return nil, err
		}
		var reply spec.ResourceResolveReply
		if len(res) > 0 {
			if err := json.Unmarshal(res, &reply); err != nil {
				return nil, fmt.Errorf("resource resolve: decode reply: %w", err)
			}
		}
		return reply.Resolved, nil
	}
}

// --- scan legs ---

// scanLocalLeg runs the bootstrap-delicate local candy scan (parseCandyYAML→buildCandy) host-side and
// returns the unfinalized ScannedCandy map (the plugin runs the finalize + remote fetch fixpoint).
func scanLocalLeg(ctx context.Context, ex *sdk.Executor, rr spec.ResolvedProjectRequest) (map[string]spec.ScannedCandy, error) {
	var out map[string]spec.ScannedCandy
	if err := hostBuildJSON(ctx, ex, "buildengine-scan-local", rr, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// scanSeamsLeg wires the three host-coupled ScanSeams legs (the reachability-scoped remote-ref walk,
// the git clone/cache, the per-candy remote manifest scan) the plugin's loaderkit.ScanCandyFromLocal
// fetch fixpoint reaches for. localScanned reloads host-side (deterministic; identical to the plugin's).
// EnsureRepo/ScanRemote are the cfg-agnostic shared host legs (hostEnsureRepoLeg/hostScanRemoteLeg),
// reused by namespaceScanSeams (R3 — ONE copy of each).
func scanSeamsLeg(ctx context.Context, ex *sdk.Executor, rr spec.ResolvedProjectRequest) loaderkit.ScanSeams {
	return loaderkit.ScanSeams{
		CollectRemoteRefs: func(_ map[string]spec.ScannedCandy) ([]loaderkit.RemoteDownload, error) {
			var out []loaderkit.RemoteDownload
			if err := hostBuildJSON(ctx, ex, "buildengine-collect-remote-refs", rr, &out); err != nil {
				return nil, err
			}
			return out, nil
		},
		EnsureRepo: hostEnsureRepoLeg(ctx, ex),
		ScanRemote: hostScanRemoteLeg(ctx, ex),
	}
}

// hostEnsureRepoLeg / hostScanRemoteLeg are the cfg-agnostic ScanSeams closures shared by the ROOT
// scan (scanSeamsLeg) and the NAMESPACED scan (namespaceScanSeams): EnsureRepo downloads+caches a
// repo (buildengine-ensure-repo — git cache, cfg-independent); ScanRemote scans the cached repo for
// the wanted bare refs (buildengine-scan-remote — parseCandyYAML registry scan, cfg-independent).
// Only CollectRemoteRefs differs between the two scans (root: reachability walk via
// buildengine-collect-remote-refs; namespaced: the host-pre-computed namespace-scoped set).
func hostEnsureRepoLeg(ctx context.Context, ex *sdk.Executor) func(repoPath, version string) (string, error) {
	return func(repoPath, version string) (string, error) {
		var out struct {
			Dir string `json:"dir"`
		}
		if err := hostBuildJSON(ctx, ex, "buildengine-ensure-repo", map[string]string{"repo": repoPath, "version": version}, &out); err != nil {
			return "", err
		}
		return out.Dir, nil
	}
}

func hostScanRemoteLeg(ctx context.Context, ex *sdk.Executor) func(cacheDir, repoPath string, wantRefs map[string]bool) (map[string]spec.ScannedCandy, error) {
	return func(cacheDir, repoPath string, wantRefs map[string]bool) (map[string]spec.ScannedCandy, error) {
		refs := make([]string, 0, len(wantRefs))
		for r := range wantRefs {
			refs = append(refs, r)
		}
		var out map[string]spec.ScannedCandy
		if err := hostBuildJSON(ctx, ex, "buildengine-scan-remote", spec.BuildEngineScanRemoteRequest{CacheDir: cacheDir, RepoPath: repoPath, Refs: refs}, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

// namespaceScanSeams builds the loaderkit.ScanSeams for a namespaced candy-scan fix-point over the
// host-pre-computed per-namespace inputs: CollectRemoteRefs returns the host's namespace-scoped
// downloads verbatim (the ONE cfg-coupled step, already done host-side by CollectRemoteRefsOpts over
// the namespace's own cfg); EnsureRepo/ScanRemote reuse the cfg-agnostic shared host legs for the
// transitive fetch. The plugin never re-loads the namespace cfg or re-walks its reachability — the
// host did that once and emitted the flat NamespaceScanReply.
func namespaceScanSeams(ctx context.Context, ex *sdk.Executor, downloads []spec.RemoteDownload) loaderkit.ScanSeams {
	return loaderkit.ScanSeams{
		CollectRemoteRefs: func(_ map[string]spec.ScannedCandy) ([]loaderkit.RemoteDownload, error) {
			return downloads, nil
		},
		EnsureRepo: hostEnsureRepoLeg(ctx, ex),
		ScanRemote: hostScanRemoteLeg(ctx, ex),
	}
}

// resolveNamespaceUFByPath descends the root uf.Namespaces tree by the dotted child path ("fedora"
// / "ns1.ns2") to the namespace's *spec.UnifiedFile, recovering the namespace's own *spec.Config
// (via ProjectConfig()) for FillNamespaceBoxViews plugin-side. sub is deliberately NOT carried in
// the wire reply — spec.Config is hand-written with a recursive Namespaces map (not cleanly
// serializable); the plugin derives it from the root uf the seam already holds + the entry's child.
func resolveNamespaceUFByPath(rootUF *spec.UnifiedFile, child string) *spec.UnifiedFile {
	if child == "" {
		return rootUF
	}
	uf := rootUF
	for _, part := range strings.Split(child, ".") {
		next := uf.Namespaces[part]
		if next == nil {
			return nil
		}
		uf = next
	}
	return uf
}

// --- validate + prep legs ---

// validateProjectLeg runs the pre-build validation GATE via InvokeProvider(command:validate) — the
// plugin↔plugin form of the former host validateProjectForBuild (its comment named exit "K3").
func validateProjectLeg(ctx context.Context, ex *sdk.Executor, rr spec.ResolvedProjectRequest) error {
	params, err := json.Marshal(spec.ValidateProjectRequest{Dir: rr.Dir, IncludeDisabled: rr.IncludeDisabled})
	if err != nil {
		return err
	}
	res, err := ex.InvokeProvider(ctx, "command", "validate", sdk.OpValidate, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return err
	}
	var diags spec.Diagnostics
	if len(res) > 0 {
		if err := json.Unmarshal(res, &diags); err != nil {
			return fmt.Errorf("pre-build validation: decode diagnostics: %w", err)
		}
	}
	var msgs []string
	for _, it := range diags.Items {
		if it.Severity == "warning" {
			continue
		}
		msgs = append(msgs, it.Message)
	}
	switch len(msgs) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("validation error: %s", msgs[0])
	default:
		out := "validation errors:"
		for _, m := range msgs {
			out += "\n  " + m
		}
		return fmt.Errorf("%s", out)
	}
}

// renderSeamPrepLeg (HostBuild("buildengine-prep")) is DELETED in K-wave 2 cone R1. It pushed the
// plugin-resolved boxes into the host's render-seam Generator cache; that cache existed only for the
// inline-builder / ensure-builders render seams, both of which now peer-dispatch via InvokeProvider,
// so nothing read it. The host leg went with it (charly/host_build_buildengine.go).

// inspectUserLeg probes an external base image for a uid account via InvokeProvider(verb:oci)
// (oci_op=inspect-user) — the plugin-side resolveUserContext external-base branch.
func inspectUserLeg(ctx context.Context, ex *sdk.Executor, ref string, uid int) (spec.UserInfo, error) {
	params, err := json.Marshal(spec.ImageUserInput{Ref: ref, UID: uid})
	if err != nil {
		return spec.UserInfo{}, err
	}
	env, err := json.Marshal(map[string]string{"oci_op": "inspect-user"})
	if err != nil {
		return spec.UserInfo{}, err
	}
	res, err := ex.InvokeProvider(ctx, "verb", "oci", sdk.OpRun, params, env, sdk.InvokeProviderOpts{})
	if err != nil {
		return spec.UserInfo{}, err
	}
	var info spec.UserInfo
	if len(res) > 0 {
		if err := json.Unmarshal(res, &info); err != nil {
			return spec.UserInfo{}, fmt.Errorf("oci inspect-user: decode reply: %w", err)
		}
	}
	return info, nil
}

// --- envelope assembler (plugin seams) ---

// projectResolvedProjectLeg calls the SHARED loaderkit.ProjectResolvedProject assembler (U2) with
// PLUGIN-supplied ResolveProjectSeams: ResolveBox is pure buildkit; ResolveResources rides
// InvokeProvider(kind:resource); FillNamespacedBoxes fetches the host's FLAT NamespaceScanReply
// (buildengine-namespaced — the host recurses the import-namespace tree ONCE, emitting per-namespace
// pre-fix-point scanned candies + the namespace-scoped initial remote downloads) and folds each
// entry plugin-side (loaderkit.ScanCandyFromLocal + deploykit.RawCandyPair +
// deploykit.FillNamespaceBoxViews — the deploykit calls relocated out of the deleted
// charly/resolved_project_host.go namespaced-box fill); ComputeIntermediates/
// ShouldIncludeDisabled/ExternalizedBuilders are pure. preResolvedBoxes carries the render-prep
// caches so the envelope preserves them.
// diags, when non-nil, makes the resolve TOLERANT (loaderkit.ProjectResolvedProject skips a box
// whose ResolveBox fails instead of aborting — mirroring the deleted host projector's tolerant
// branch, buildResolvedProjectTolerant); pass nil for the FAIL-FAST behavior 3b's resolveProjectEnvelope
// relies on (byte-for-byte parity with the original host projector).
func projectResolvedProjectLeg(ctx context.Context, ex *sdk.Executor, cfg *spec.Config, layers map[string]spec.CandyReader, uf *spec.UnifiedFile, distroCfg *spec.DistroConfig, builderCfg *spec.BuilderConfig, initCfg *buildkit.InitConfig, dir, version, calver string, includeDisabled bool, preResolvedBoxes map[string]*buildkit.ResolvedBox, diags *spec.Diagnostics) (*spec.ResolvedProject, error) {
	includeNames := map[string]bool{}
	seams := loaderkit.ResolveProjectSeams{
		ResolveBox: func(c *spec.Config, name, cv, d string) (*buildkit.ResolvedBox, error) {
			return buildkit.ResolveBox(c, name, cv, d, buildkit.ResolveOpts{IncludeDisabled: includeDisabled, DistroCfg: distroCfg, BuilderCfg: builderCfg})
		},
		FillNamespacedBoxes: func(rootUF *spec.UnifiedFile, ic *buildkit.InitConfig, prefix, cv, d string, rp *spec.ResolvedProject, _ map[*spec.UnifiedFile]bool) {
			if prefix != "" {
				return // the host leg does the full namespace recursion; only the root call dispatches
			}
			var reply spec.NamespaceScanReply
			if err := hostBuildJSON(ctx, ex, "buildengine-namespaced", spec.BuildResolveRequest{Dir: d, Tag: cv, IncludeDisabled: includeDisabled}, &reply); err != nil {
				return // best-effort/additive, matching the host fill's tolerance
			}
			foldNamespaceScanEntries(ctx, ex, rootUF, ic, cv, d, includeDisabled, distroCfg, builderCfg, reply, rp)
		},
		ResolveResources: func(u *spec.UnifiedFile) map[string]*spec.ResolvedResource {
			return spec.ResolvePluginKindViaPlugin(u, "resource", resolveResourceLeg(ctx, ex))
		},
		ShouldIncludeDisabled: func(name string) bool {
			if !includeDisabled {
				return false
			}
			if len(includeNames) == 0 {
				return true
			}
			return includeNames[name]
		},
		ComputeIntermediates: func(boxes map[string]*buildkit.ResolvedBox, l map[string]spec.CandyReader, c *spec.Config, tag string) (map[string]*buildkit.ResolvedBox, error) {
			return deploykit.ComputeIntermediates(boxes, l, intermediateDefaults(c), tag)
		},
		ExternalizedBuilders: buildkit.ExternalizedBuilders,
	}
	return loaderkit.ProjectResolvedProject(cfg, layers, uf, distroCfg, builderCfg, initCfg, dir, version, calver, seams, diags, preResolvedBoxes)
}

// foldNamespaceScanEntries is the plugin-side fold over the host's NamespaceScanReply: for each
// namespace, descend the root uf.Namespaces tree by the entry's child path to recover the
// namespace's *spec.Config, run the candy-scan fetch fix-point (loaderkit.ScanCandyFromLocal over
// the host-pre-computed scanned + downloads + the cfg-agnostic EnsureRepo/ScanRemote host legs),
// then deploykit.RawCandyPair (fold the namespace's candies additively into rp.Candies/CandyModels,
// never overwriting) + deploykit.FillNamespaceBoxViews (fold the namespace-qualified box views into
// rp.Boxes). The deploykit calls are the deleted host namespaced-box fill's deploykit calls,
// relocated plugin-side (candy/plugin-build is the legit deploykit owner; no new importer). cv/d
// come from the seam closure's resolve context; vopts is rebuilt plugin-side from the resolve
// context's distroCfg/builderCfg/ic — byte-identical to the deleted host's resolveVocabOpts(dir,
// opts) result (boxResolveOpts leaves DistroCfg/BuilderCfg nil, so resolveVocabOpts fills them
// from the SAME build vocab the plugin's distroCfg/builderCfg carry).
func foldNamespaceScanEntries(ctx context.Context, ex *sdk.Executor, rootUF *spec.UnifiedFile, ic *buildkit.InitConfig, cv, d string, includeDisabled bool, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, reply spec.NamespaceScanReply, rp *spec.ResolvedProject) {
	vopts := spec.ResolveOpts{IncludeDisabled: includeDisabled, DistroCfg: distroCfg, BuilderCfg: builderCfg, InitCfg: ic}
	for _, entry := range reply.Entries {
		subUF := resolveNamespaceUFByPath(rootUF, entry.Child)
		if subUF == nil {
			continue
		}
		sub := subUF.ProjectConfig()
		nsLayers, err := loaderkit.ScanCandyFromLocal(entry.Scanned, ic, namespaceScanSeams(ctx, ex, entry.Downloads))
		if err != nil {
			continue // best-effort/additive, matching the deleted host fill's tolerance
		}
		for name, c := range nsLayers {
			if c == nil {
				continue
			}
			m, v, ok := deploykit.RawCandyPair(c)
			if !ok {
				continue
			}
			if rp.Candies == nil {
				rp.Candies = map[string]spec.CandyView{}
				rp.CandyModels = map[string]spec.CandyModel{}
			}
			if _, exists := rp.CandyModels[name]; !exists {
				rp.Candies[name] = v
				rp.CandyModels[name] = m
			}
		}
		deploykit.FillNamespaceBoxViews(sub, nsLayers, ic, entry.Child, cv, d, vopts, rp)
	}
}
