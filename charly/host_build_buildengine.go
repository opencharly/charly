package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/spec/spec"
)

// host_build_buildengine.go — the thin `buildengine-*` reverse legs the PLUGIN-SIDE build-engine
// RESOLVE (candy/plugin-build/resolve.go, K3 U6) reaches the host for. Each wraps exactly one
// genuinely host-coupled step a sdk-only candy cannot do: the bootstrap-delicate local candy scan
// (parseCandyYAML→buildCandy, the B bootstrap root), the git clone/cache + remote manifest scan, the
// build-time plugin CONNECT (registry M), and the namespaced-box pre-computation (embeds a nested
// scan+render-prep). These legs REPLACE the former fat host build-prep seam (hostBuildBuildResolve,
// DELETED) — the resolve ORCHESTRATION + drive-model run plugin-side. K-wave 2 cone R1 dropped the
// LAST prep leg (hostBuildPrep/"buildengine-prep"): the host-fs half had already moved to
// candy/plugin-build, leaving it populating a render-seam Generator cache that no longer has any
// reader, since every render seam now peer-dispatches instead of calling back.
//
// Class-generic action-noun kinds (never provider words — the F11 uniform-API gate), mirroring
// host_build_loader_floor.go's loader-* legs one level up the stack.

// hostBuildScanLocal runs the local candy scan host-side (RegisterBuildVocabulary is applied first so
// parseCandyYAML classifies distro/format keys) and returns the UNFINALIZED ScannedCandy map. The
// plugin runs the finalize + remote-fetch fixpoint (loaderkit.ScanCandyFromLocal).
func hostBuildScanLocal(_ context.Context, req spec.ResolvedProjectRequest, _ buildEngineContext) (map[string]spec.ScannedCandy, error) {
	dir := reqDirOrCwd(req.Dir)
	if distroCfg, _, _, err := LoadDefaultBuildConfig(dir); err == nil && distroCfg != nil {
		RegisterBuildVocabulary(distroCfg)
	}
	return scanLocalCandies(dir)
}

// hostBuildCollectRemoteRefs runs the reachability-scoped remote-ref walk (the ScanSeams.CollectRemoteRefs
// leg). It reloads cfg + the local scan host-side (deterministic — identical to the plugin's), so the
// wrapped-view walk sees the SAME withLocalRawRefs augmentation the in-core scan used. opts is built
// via boxResolveOpts (the SINGLE source of the box-selection rule, R3) so req.RequestedBoxes reaches
// CollectRemoteRefsOpts' own RequestedBoxes handling (task #17 fix) exactly the way it already
// reaches buildkit.ResolveAllBox's — an on-demand namespace-qualified target
// (`charly box generate fedora.check-pod`) is otherwise never visited by the reachability walk, and
// its own remote candy refs are silently never fetched.
func hostBuildCollectRemoteRefs(_ context.Context, req spec.ResolvedProjectRequest, _ buildEngineContext) ([]spec.RemoteDownload, error) {
	dir := reqDirOrCwd(req.Dir)
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	localScanned, err := scanLocalCandies(dir)
	if err != nil {
		return nil, err
	}
	opts := boxResolveOpts(req.RequestedBoxes, req.IncludeDisabled)
	opts.ExtraCandyRefs = req.ExtraCandyRefs
	return CollectRemoteRefsOpts(cfg, requireProjectLoader().FinalizeScannedCandies(localScanned, nil), withLocalRawRefs(opts, localScanned))
}

// hostBuildEnsureRepo resolves a (repo, version) to a local cache dir (the ScanSeams.EnsureRepo leg),
// fetching + auto-migrating on a cache miss.
func hostBuildEnsureRepo(_ context.Context, req map[string]string, _ buildEngineContext) (map[string]string, error) {
	dir, err := EnsureRepoDownloaded(req["repo"], req["version"])
	if err != nil {
		return nil, err
	}
	return map[string]string{"dir": dir}, nil
}

// hostBuildScanRemote scans the wanted bare refs out of a downloaded repo cache (the ScanSeams.ScanRemote
// leg), driving parseCandyYAML through the shared CandyScanner.
func hostBuildScanRemote(_ context.Context, req spec.BuildEngineScanRemoteRequest, _ buildEngineContext) (map[string]spec.ScannedCandy, error) {
	wantRefs := make(map[string]bool, len(req.Refs))
	for _, r := range req.Refs {
		wantRefs[r] = true
	}
	return requireCandyScanner().ScanRemoteCandy(req.CacheDir, req.RepoPath, wantRefs, parseCandyYAML)
}

// hostBuildConnectPlugins connects the project's build-time (out-of-tree) plugin candies into the host
// registry so the plugin's subsequent InvokeProvider/render dispatch reaches them (registry M). Best-
// effort: a plugin the build actually USES fails loudly later at ops.OpEmit/ops.OpResolve. opts is
// built via boxResolveOpts (R3, the SAME helper hostBuildCollectRemoteRefs uses) so req.RequestedBoxes
// reaches this scan's own CollectRemoteRefsOpts call too (task #17 fix) — an on-demand
// namespace-qualified build/generate target otherwise never gets its own build-time plugin candies
// discovered/connected here either, the sibling of the "unknown candy" gap this task closes.
func hostBuildConnectPlugins(_ context.Context, req spec.ResolvedProjectRequest, _ buildEngineContext) (map[string]string, error) {
	dir := reqDirOrCwd(req.Dir)
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	opts := boxResolveOpts(req.RequestedBoxes, req.IncludeDisabled)
	opts.ExtraCandyRefs = req.ExtraCandyRefs
	if _, _, initCfg, derr := LoadDefaultBuildConfig(dir); derr == nil {
		opts.InitCfg = initCfg
	}
	layers, err := ScanAllCandyWithConfigOpts(dir, cfg, opts)
	if err != nil {
		return nil, err
	}
	buildRefs := collectReferencedPluginWords(layers, cfg.Box, req.ExtraCandyRefs)
	if perr := loadProjectPlugins(context.Background(), layers, buildRefs); perr != nil {
		fmt.Fprintf(os.Stderr, "warning: build-time plugin load: %v\n", perr)
	}
	return map[string]string{}, nil
}

// hostBuildNamespaced recurses the project's import-namespace tree ONCE and returns a FLAT
// spec.NamespaceScanReply: one entry per namespace carrying its PRE-fix-point scanned candies +
// the namespace-scoped INITIAL remote-download set (the ResolveProjectSeams.FillNamespacedBoxes leg
// — K1 loader-cone fabric-tail, #55). The plugin (candy/plugin-build's FillNamespacedBoxes seam)
// iterates the list and runs the candy-scan fetch fix-point (loaderkit.ScanCandyFromLocal) +
// deploykit.RawCandyPair + deploykit.FillNamespaceBoxViews plugin-side — the deploykit calls left
// core when charly/resolved_project_host.go was deleted (the file's own L26-32 named exit). The
// host keeps ONLY the genuinely host-coupled steps: projectCandiesScanned (reads subUF.Candy
// in-memory — the R1 fix) + the reachability walk (CollectRemoteRefsOpts over the namespace's own
// cfg). initCfg/calver/dir come from the plugin's own resolve context and are NOT duplicated here.
// Best-effort/additive: a project-less or unresolvable dir returns an empty reply.
func hostBuildNamespaced(_ context.Context, req spec.BuildResolveRequest, _ buildEngineContext) (spec.NamespaceScanReply, error) {
	dir := reqDirOrCwd(req.Dir)
	opts := boxResolveOpts(nil, req.IncludeDisabled)
	lp, err := loadProjectForResolve(dir, opts, nil)
	if err != nil || lp.empty || lp.uf == nil {
		return spec.NamespaceScanReply{}, nil // best-effort/additive
	}
	reply := spec.NamespaceScanReply{}
	collectNamespaceScanEntries(lp.uf, "", dir, opts, &reply, map[*spec.UnifiedFile]bool{})
	return reply, nil
}

// collectNamespaceScanEntries recurses uf.Namespaces and appends one spec.NamespaceScanEntry per
// namespace (FLAT, nested-qualified by child = prefix+"."+ns) to reply. dir is the OUTER project
// dir — the fallback for a namespace's own RootDir when resolving a discovered candy's From: path
// (a namespace's candy paths are relative to the NAMESPACE's root, not the caller's; matches the
// deleted host namespaced-box fill's nsDir fallback). The visited cycle-guard mirrors the deleted fill.
// A namespace whose candy scan fails contributes an empty (skipped) entry — best-effort/additive,
// never fatal, matching the deleted fill's tolerance.
func collectNamespaceScanEntries(uf *spec.UnifiedFile, prefix, dir string, opts spec.ResolveOpts, reply *spec.NamespaceScanReply, visited map[*spec.UnifiedFile]bool) {
	if uf == nil || visited[uf] {
		return
	}
	visited[uf] = true
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
			nsDir = dir
		}
		// projectCandiesScanned reads subUF.Candy directly (no re-load, no directory mismatch —
		// the R1 fix preserved from the deleted host namespaced-box fill): covers BOTH a namespace's
		// own local discover:-found candies AND its inline candy: nodes.
		var scanned map[string]spec.ScannedCandy
		if localScanned, lErr := projectCandiesScanned(subUF, nsDir); lErr == nil {
			scanned = localScanned
		}
		// The ONE cfg-coupled step: the namespace-scoped reachability walk over the namespace's
		// own cfg + its candies' raw @-refs — byte-identical to scanSeamsFor(sub,
		// opts).CollectRemoteRefs(scanned) (the deleted host namespaced-box fill's scan seam): it
		// runs UNCONDITIONALLY, because CollectRemoteRefsOpts walks sub.Box (the namespace's boxes)
		// to collect the boxes' candy @-refs — independent of whether the namespace has ANY local
		// candies. A namespace that VENDORS NO candies of its own but whose boxes pin remote candies
		// (the distro-fedora case: every candy is an @github.com/opencharly/charly/candy/<name>:<tag>
		// ref) still has box candy refs to fetch; the former `if len(scanned) > 0` guard skipped the
		// walk for such a namespace, dropping the box candy refs so the plugin's fix-point fetched
		// nothing → "candy not found" on every namespaced box (R1 RCA: bisect first-bad = coneK1load
		// b367e5d5). The plugin's ScanSeams.CollectRemoteRefs returns this verbatim;
		// EnsureRepo/ScanRemote still round-trip to the cfg-agnostic host legs for the transitive
		// fetch. FinalizeScannedCandies / withLocalRawRefs are nil/empty-safe (range over nil is a
		// no-op), so an empty `scanned` degrades to a boxes-only walk — exactly origin/main's shape.
		downloads, _ := CollectRemoteRefsOpts(sub, requireProjectLoader().FinalizeScannedCandies(scanned, nil), withLocalRawRefs(opts, scanned))
		reply.Entries = append(reply.Entries, spec.NamespaceScanEntry{
			Child:     child,
			Scanned:   scanned,
			Downloads: downloads,
		})
		collectNamespaceScanEntries(subUF, child, dir, opts, reply, visited)
	}
}

// The "buildengine-prep" leg is DELETED (K-wave 2 cone R1). Its host-fs half
// (cleanStaleBuildDirs/writeContextIgnore/createRemoteCandyCopies/ensureCharlyBinaryFresh) had
// already moved to candy/plugin-build/host_prep.go (K3), leaving it doing exactly ONE thing:
// building a candy-scan-only Generator to populate the render-seam floor's renderGenCache. With the
// render-seam host-builder itself gone (no render method needs a host callback any more), that cache
// had no readers, so the leg was pure waste — a full local candy scan per build for a value nothing
// consumed. The plugin-side call in candy/plugin-build/resolve_legs.go went with it.

// hostBuildContextIgnoreBaseline returns the bootstrap-embedded context_ignore_baseline patterns (a
// D-category static fact baked into the charly binary's embedded charly.yml) — the ONE piece of
// writeContextIgnore's former host dependency a plugin genuinely cannot read itself (a SEPARATE Go
// module/binary has no access to charly/charly.yml's //go:embed). Everything else writeContextIgnore
// needs (cfg.Defaults.ContextIgnore, the write targets) is already plugin-side.
func hostBuildContextIgnoreBaseline(_ context.Context, _ struct{}, _ buildEngineContext) ([]string, error) {
	return baselineContextIgnore, nil
}

// baselineContextIgnore is the parsed directive the leg above serves. It lives HERE, with its only
// reader, since K-wave 2 cone R1 folded it out of the deleted charly/generate.go. Parsed once at
// package-var init; a malformed or empty embed PANICS — this is a build-time invariant of the
// binary's own embedded charly.yml, never a runtime input a project can influence.
var baselineContextIgnore = parseEmbeddedContextIgnoreBaseline()

func parseEmbeddedContextIgnoreBaseline() []string {
	var doc struct {
		ContextIgnoreBaseline []string `yaml:"context_ignore_baseline"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.ContextIgnoreBaseline) == 0 {
		panic("host build: embedded charly.yml has no context_ignore_baseline: directive")
	}
	return doc.ContextIgnoreBaseline
}

// reqDirOrCwd resolves an empty request dir to the process cwd (the same fallback every build-engine
// host-builder uses).
func reqDirOrCwd(dir string) string {
	if dir != "" {
		return dir
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return dir
}

// boxResolveOpts builds the spec.ResolveOpts that scope a generate/build to a set of
// explicitly-named boxes. It is the SINGLE source of the box-selection rule for
// both `charly box build` and `charly box generate` (R3): an empty slice means
// "all enabled boxes" (no scoping); a non-empty slice pins those names into the
// resolved set (RequestedBoxes) and, when --include-disabled is set, relaxes the
// enabled: false gate for exactly those names (IncludeDisabledNames) so the
// override never widens the working set globally. Callers pass boxes already run
// through buildkit.NormalizeBoxArgs.
func boxResolveOpts(boxes []string, includeDisabled bool) spec.ResolveOpts {
	opts := spec.ResolveOpts{IncludeDisabled: includeDisabled}
	if len(boxes) == 0 {
		return opts
	}
	opts.RequestedBoxes = boxes
	if includeDisabled {
		opts.IncludeDisabledNames = make(map[string]bool, len(boxes))
		for _, name := range boxes {
			opts.IncludeDisabledNames[name] = true
		}
	}
	return opts
}

// ensureCharlyBinaryFresh (the FS-prep quartet — cleanStaleBuildDirs/writeContextIgnore/
// createRemoteCandyCopies/ensureCharlyBinaryFresh) moved to candy/plugin-build (K3 host-prep move,
// coneB-render): pure filesystem/exec operations over data (cfg/layers/resolved) the plugin already
// computes in resolveBuildEngine — no host-only dependency. See candy/plugin-build/host_prep.go.

// Register the buildengine-* legs at package-var init (before any init()).
var _ = func() bool {
	registerHostBuilder("buildengine-scan-local", typedHostBuilder("buildengine-scan-local", hostBuildScanLocal))
	registerHostBuilder("buildengine-collect-remote-refs", typedHostBuilder("buildengine-collect-remote-refs", hostBuildCollectRemoteRefs))
	registerHostBuilder("buildengine-ensure-repo", typedHostBuilder("buildengine-ensure-repo", hostBuildEnsureRepo))
	registerHostBuilder("buildengine-scan-remote", typedHostBuilder("buildengine-scan-remote", hostBuildScanRemote))
	registerHostBuilder("buildengine-connect-plugins", typedHostBuilder("buildengine-connect-plugins", hostBuildConnectPlugins))
	registerHostBuilder("buildengine-namespaced", typedHostBuilder("buildengine-namespaced", hostBuildNamespaced))
	registerHostBuilder("buildengine-context-ignore-baseline", typedHostBuilder("buildengine-context-ignore-baseline", hostBuildContextIgnoreBaseline))
	return true
}()
