package build

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// resolve_legs.go — the plugin-side leg helpers the build-engine RESOLVE (resolve.go, U6) reaches the
// host for. Each is a thin HostBuild / InvokeProvider wrapper over a charly `buildengine-*` host leg
// (charly/host_build_buildengine.go) or a compiled-in peer plugin. The pattern mirrors the K1 loader
// witness legs (candy/plugin-fleet) — only the genuinely host-coupled steps cross the wire.

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

// scanLocalLeg scans the project's OWN candies into their unfinalized ScannedCandy form (the plugin
// runs the finalize + remote fetch fixpoint over the result). It runs PLUGIN-SIDE over
// loaderkit.ProjectCandiesScanned since K-wave 2 cone R1 (A2 unit 3); the `buildengine-scan-local`
// host leg is DELETED.
//
// The leg existed because the scan's manifest parse appeared to need charly's clause-B buildCandy
// factory — disproven by the unit-2 corpus spike — and because only the host held a loaded project.
// Neither holds now: the caller already has `uf` from loaderkit.LoadUnifiedViaExecutor, whose walk
// runs `discover:` at each project boundary exactly as the host's own LoadUnified + ApplyDiscover
// pair did, so uf.Candy carries the discovered candies before this is called.
//
// The host leg's project-less fallback (legacyScanCandiesDirScanned, for a dir with no charly.yml)
// has no plugin-side analogue and needs none: every caller returns the empty-project envelope before
// reaching here when LoadUnifiedViaExecutor reports no project.
func scanLocalLeg(ctx context.Context, ex *sdk.Executor, uf *spec.UnifiedFile, dir string, distroCfg *spec.DistroConfig) (map[string]spec.ScannedCandy, error) {
	return loaderkit.ProjectCandiesScanned(uf, dir, parseCandyManifestLeg(ctx, ex, distroCfg))
}

// scanSeamsLeg wires the ScanSeams legs the plugin's loaderkit.ScanCandyFromLocal fetch fixpoint
// reaches for. Since K-wave 2 cone R1 (A2) only ONE of the three still crosses to the host:
//
//   - CollectRemoteRefs runs PLUGIN-SIDE over loaderkit.CollectRemoteRefsOpts with the resolve's own
//     cfg + the fixpoint's own localScanned + the shared executor-backed refs legs. The former
//     `buildengine-collect-remote-refs` host leg re-derived BOTH inputs host-side (LoadConfig +
//     scanLocalCandies) purely so core could hand them back to the same mechanism — core was calling
//     a mechanism, not defining one, so by the defines-vs-calls test it was an R-item. The plugin
//     already holds cfg (uf.ProjectConfig, resolve.go step 1) and receives localScanned as the
//     seam's own parameter, so nothing needs re-deriving: this is byte-identical to the host leg's
//     body (same FinalizeScannedCandies(…, nil) throwaway wrap, same WithLocalRawRefs augmentation,
//     same BoxResolveOpts-scoped opts carrying RequestedBoxes/ExtraCandyRefs — task #17's fix).
//   - EnsureRepo likewise runs plugin-side over loaderkit.EnsureRepoDownloaded (hostEnsureRepoLeg
//     and its `buildengine-ensure-repo` host leg are both deleted).
//   - ScanRemote runs plugin-side too (scanRemoteLeg over parseCandyManifestLeg). It was the LAST
//     leg still crossing: its per-candy manifest parse appeared to need charly's clause-B buildCandy
//     factory, which an RDD spike over the whole 324-manifest corpus disproved (the round trip
//     through it was an identity). `buildengine-scan-remote` died with it.
//
// So NONE of the three ScanSeams legs is a host round-trip any more.
func scanSeamsLeg(ctx context.Context, ex *sdk.Executor, rr spec.ResolvedProjectRequest, cfg *spec.Config, distroCfg *spec.DistroConfig) loaderkit.ScanSeams {
	return loaderkit.ScanSeams{
		CollectRemoteRefs: func(localScanned map[string]spec.ScannedCandy) ([]loaderkit.RemoteDownload, error) {
			opts := spec.BoxResolveOpts(rr.RequestedBoxes, rr.IncludeDisabled)
			opts.ExtraCandyRefs = rr.ExtraCandyRefs
			return loaderkit.CollectRemoteRefsOpts(
				cfg,
				loaderkit.FinalizeScannedCandies(localScanned, nil),
				spec.WithLocalRawRefs(opts, localScanned),
				loaderkit.RefsSeamsFromExecutor(ctx, ex),
			)
		},
		EnsureRepo: ensureRepoLeg(ctx, ex),
		ScanRemote: scanRemoteLeg(parseCandyManifestLeg(ctx, ex, distroCfg)),
	}
}

// ensureRepoLeg resolves a (repo, version) to a local cache dir, fetching + auto-migrating on a
// cache miss — PLUGIN-SIDE over the shared loaderkit mechanism + the executor-backed refs legs. It
// is cfg-agnostic, so both the ROOT scan (scanSeamsLeg) and the NAMESPACED scan (namespaceScanSeams)
// share this one copy (R3).
//
// The seams are built INSIDE the closure, once PER CALL — never hoisted to construction time. The
// deleted `buildengine-ensure-repo` host leg read CHARLY_REPO_OVERRIDE (RefsCollectSeams.
// OverrideEnvValue, an os.Getenv) at the moment of each fetch, and resolveProjectEnvelope's
// LocalSuperproject branch sets that env var around the resolve with a deferred restore. Caching
// the seams would freeze the override at whatever the env held when the scan seams were assembled,
// silently changing which tree a fetch resolves against; per-call construction is byte-identical to
// the leg it replaces.
func ensureRepoLeg(ctx context.Context, ex *sdk.Executor) func(repoPath, version string) (string, error) {
	return func(repoPath, version string) (string, error) {
		return loaderkit.EnsureRepoDownloaded(repoPath, version, loaderkit.RefsSeamsFromExecutor(ctx, ex))
	}
}

// parseCandyManifestLeg builds the per-document candy-manifest parse the scan mechanisms take as
// their `parseDoc` seam — PLUGIN-SIDE over loaderkit.ParseCandyManifest (K-wave 2 cone R1, A2 unit
// 2). It used to be unreachable from a plugin, which is the only reason `buildengine-scan-remote`
// existed: the parse appeared to need charly's clause-B buildCandy factory. An RDD spike over the
// whole 324-manifest corpus proved that dependency was a pn->genericNode->pn identity round trip
// (321 node-form manifests plus all 3 error paths, byte-identical), so the mechanism relocated and
// the leg died with it.
//
// The two host-side values it needs are fetched ONCE per scan and captured: the registry-derived
// kind-recognition snapshot over the EXISTING `loader-threaded` host leg, and the build vocabulary
// derived from the resolve's own distroCfg — the same DistroConfig the deleted host leg re-derived
// via LoadDefaultBuildConfig before calling RegisterBuildVocabulary.
func parseCandyManifestLeg(ctx context.Context, ex *sdk.Executor, distroCfg *spec.DistroConfig) func(string) (*spec.Candy, error) {
	threaded := loaderkit.LoaderThreadedViaExecutor(ctx, ex)
	vocab := spec.NewCandyVocab(distroCfg)
	return func(path string) (*spec.Candy, error) {
		return loaderkit.ParseCandyManifest(path, threaded, vocab)
	}
}

// scanRemoteLeg scans the wanted bare refs out of a downloaded repo cache — PLUGIN-SIDE over
// loaderkit.ScanRemoteCandy with the parse leg above. cfg-agnostic, so the root and namespaced scans
// share this one copy (R3).
func scanRemoteLeg(parseDoc func(string) (*spec.Candy, error)) func(cacheDir, repoPath string, wantRefs map[string]bool) (map[string]spec.ScannedCandy, error) {
	return func(cacheDir, repoPath string, wantRefs map[string]bool) (map[string]spec.ScannedCandy, error) {
		return loaderkit.ScanRemoteCandy(cacheDir, repoPath, wantRefs, parseDoc)
	}
}

// namespaceScanSeams builds the loaderkit.ScanSeams for a namespaced candy-scan fix-point over the
// per-namespace downloads the caller already walked: CollectRemoteRefs returns that namespace-scoped
// set verbatim (the reachability walk runs ONCE per namespace in fillNamespacedBoxes, over the
// namespace's own cfg); EnsureRepo/ScanRemote reuse the cfg-agnostic shared legs for the transitive
// fetch. Nothing here crosses to the host.
func namespaceScanSeams(ctx context.Context, ex *sdk.Executor, downloads []spec.RemoteDownload, distroCfg *spec.DistroConfig) loaderkit.ScanSeams {
	return loaderkit.ScanSeams{
		CollectRemoteRefs: func(_ map[string]spec.ScannedCandy) ([]loaderkit.RemoteDownload, error) {
			return downloads, nil
		},
		EnsureRepo: ensureRepoLeg(ctx, ex),
		ScanRemote: scanRemoteLeg(parseCandyManifestLeg(ctx, ex, distroCfg)),
	}
}

// --- validate + prep legs ---

// validateProjectLeg runs the pre-build validation GATE via InvokeProvider(command:validate) — the
// plugin↔plugin form of the former host-side gate (whose comment named exit "K3"). Core carries no
// production copy of this dispatch any more; the only remaining one is the fixture-test harness in
// charly/validate_dispatch_test.go, which mirrors this function deliberately.
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
// InvokeProvider(kind:resource); FillNamespacedBoxes recurses the import-namespace tree ENTIRELY
// plugin-side (fillNamespacedBoxes — scan, reachability walk, fetch fix-point and fold in ONE pass;
// the former `buildengine-namespaced` host leg and its NamespaceScanReply wire hop are DELETED);
// ComputeIntermediates/
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
		FillNamespacedBoxes: func(rootUF *spec.UnifiedFile, ic *buildkit.InitConfig, prefix, cv, d string, rp *spec.ResolvedProject, visited map[*spec.UnifiedFile]bool) {
			fillNamespacedBoxes(ctx, ex, rootUF, ic, prefix, cv, d, includeDisabled, distroCfg, builderCfg, rp, visited)
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

// fillNamespacedBoxes recurses uf.Namespaces and folds each import namespace's boxes (qualified) +
// its OWN candy set into rp — ENTIRELY plugin-side (K-wave 2 cone R1, A2 unit 3b). It replaces the
// former two-part shape (charly's `buildengine-namespaced` leg recursing the tree host-side to emit
// a flat spec.NamespaceScanReply, plus a plugin-side fold over that reply), which was the SAME
// recursion walked twice across a process boundary. The host leg survived only because its two
// per-namespace steps were once core-only; both are plugin-side now — the local candy scan is
// loaderkit.ProjectCandiesScanned (unit 3a) and the reachability walk is
// loaderkit.CollectRemoteRefsOpts over the shared executor-backed refs legs (unit 1) — so core was
// CALLING relocated mechanisms on the plugin's behalf over inputs the plugin already held. By the
// defines-vs-calls test that is an R-item, and the wire hop plus its #NamespaceScanReply envelope
// died with it.
//
// Behaviour is preserved verbatim from the deleted pair:
//   - the nsDir FALLBACK — a namespace's candy `from:` paths are relative to the NAMESPACE's own
//     root (subUF.RootDir), falling back to the OUTER project dir when a namespace carries none;
//   - the VISITED cycle-guard, keyed on the *spec.UnifiedFile pointer (the loader mounts a
//     back-imported ancestor as the SAME in-progress node, so a mutual import terminates here);
//   - DFS PRE-ORDER (fold a namespace, then descend into it), matching the flat reply's append order;
//   - the reachability walk runs UNCONDITIONALLY, never `if len(scanned) > 0`: CollectRemoteRefsOpts
//     walks sub.Box to collect the BOXES' candy @-refs, so a namespace that vendors no candies of its
//     own but whose boxes pin remote ones (every distro submodule) still has refs to fetch. Guarding
//     it dropped those refs and produced "candy not found" on every namespaced box (R1 RCA, first-bad
//     b367e5d5);
//   - opts is spec.BoxResolveOpts(nil, includeDisabled) — nil RequestedBoxes, so a namespace walk is
//     never narrowed by the ROOT request's box selection — and vopts carries the resolve context's
//     own distroCfg/builderCfg/ic;
//   - every step is best-effort/additive: a namespace whose scan, walk, or fix-point fails
//     contributes nothing and never aborts the resolve, and recursion into its children continues.
func fillNamespacedBoxes(ctx context.Context, ex *sdk.Executor, uf *spec.UnifiedFile, ic *buildkit.InitConfig, prefix, cv, d string, includeDisabled bool, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, rp *spec.ResolvedProject, visited map[*spec.UnifiedFile]bool) {
	if uf == nil {
		return
	}
	if visited == nil {
		visited = map[*spec.UnifiedFile]bool{}
	}
	if visited[uf] {
		return
	}
	visited[uf] = true
	opts := spec.BoxResolveOpts(nil, includeDisabled)
	vopts := spec.ResolveOpts{IncludeDisabled: includeDisabled, DistroCfg: distroCfg, BuilderCfg: builderCfg, InitCfg: ic}
	for ns, subUF := range uf.Namespaces {
		if subUF == nil {
			continue
		}
		child := ns
		if prefix != "" {
			child = prefix + "." + ns
		}
		sub := subUF.ProjectConfig()
		nsDir := subUF.RootDir
		if nsDir == "" {
			nsDir = d
		}
		// The namespace's OWN local candies — both its `discover:`-found dirs and its inline candy:
		// nodes — read straight off the already-loaded subUF (no re-load, no directory mismatch).
		var scanned map[string]spec.ScannedCandy
		if localScanned, err := loaderkit.ProjectCandiesScanned(subUF, nsDir, parseCandyManifestLeg(ctx, ex, distroCfg)); err == nil {
			scanned = localScanned
		}
		// FinalizeScannedCandies / spec.WithLocalRawRefs are nil/empty-safe, so an empty `scanned`
		// degrades to a boxes-only walk — exactly what a candy-less namespace needs.
		downloads, _ := loaderkit.CollectRemoteRefsOpts(
			sub,
			loaderkit.FinalizeScannedCandies(scanned, nil),
			spec.WithLocalRawRefs(opts, scanned),
			loaderkit.RefsSeamsFromExecutor(ctx, ex),
		)
		if nsLayers, err := loaderkit.ScanCandyFromLocal(scanned, ic, namespaceScanSeams(ctx, ex, downloads, distroCfg)); err == nil {
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
			deploykit.FillNamespaceBoxViews(sub, nsLayers, ic, child, cv, d, vopts, rp)
		}
		fillNamespacedBoxes(ctx, ex, subUF, ic, child, cv, d, includeDisabled, distroCfg, builderCfg, rp, visited)
	}
}
