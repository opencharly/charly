package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"
)

// host_build_buildengine.go — the thin `buildengine-*` reverse legs the PLUGIN-SIDE build-engine
// RESOLVE (candy/plugin-build/resolve.go, K3 U6) reaches the host for. Each wraps exactly one
// genuinely host-coupled step a sdk-only candy cannot do: the bootstrap-delicate local candy scan
// (parseCandyYAML→buildCandy, the B bootstrap root), the git clone/cache + remote manifest scan, the
// build-time plugin CONNECT (registry M), the namespaced-box pre-computation (embeds a nested
// scan+render-prep), and the host-fs PREP + the render-seam-floor NewGenerator (RULED: stays host).
// These legs REPLACE the former fat host build-prep seam (hostBuildBuildResolve, DELETED) — the
// resolve ORCHESTRATION + drive-model now run plugin-side.
//
// Class-generic action-noun kinds (never provider words — the F11 uniform-API gate), mirroring
// host_build_loader.go's loader-* legs one level up the stack.

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
// wrapped-view walk sees the SAME withLocalRawRefs augmentation the in-core scan used.
func hostBuildCollectRemoteRefs(_ context.Context, req spec.ResolvedProjectRequest, _ buildEngineContext) ([]loaderkit.RemoteDownload, error) {
	dir := reqDirOrCwd(req.Dir)
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	localScanned, err := scanLocalCandies(dir)
	if err != nil {
		return nil, err
	}
	opts := ResolveOpts{IncludeDisabled: req.IncludeDisabled, ExtraCandyRefs: req.ExtraCandyRefs}
	return CollectRemoteRefsOpts(cfg, loaderkit.FinalizeScannedCandies(localScanned, nil), withLocalRawRefs(opts, localScanned))
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
// effort: a plugin the build actually USES fails loudly later at OpEmit/OpResolve.
func hostBuildConnectPlugins(_ context.Context, req spec.ResolvedProjectRequest, _ buildEngineContext) (map[string]string, error) {
	dir := reqDirOrCwd(req.Dir)
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	opts := boxResolveOpts(nil, req.IncludeDisabled)
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

// hostBuildNamespaced pre-computes the namespace-qualified box views + each namespace's own candy scan
// (the ResolveProjectSeams.FillNamespacedBoxes leg — it embeds a nested scan + render-prep the plugin
// can't cheaply run). Returns a PARTIAL spec.ResolvedProject carrying only Boxes/Candies/CandyModels,
// which the plugin merges additively.
func hostBuildNamespaced(_ context.Context, req spec.BuildResolveRequest, _ buildEngineContext) (spec.ResolvedProject, error) {
	dir := reqDirOrCwd(req.Dir)
	opts := boxResolveOpts(nil, req.IncludeDisabled)
	lp, err := loadProjectForResolve(dir, opts, nil)
	if err != nil || lp.empty || lp.uf == nil {
		return spec.ResolvedProject{}, nil // best-effort/additive
	}
	calver := req.Tag
	if calver == "" {
		calver = ComputeCalVer()
	}
	scratch := &spec.ResolvedProject{}
	fillNamespacedBoxes(lp.uf, lp.initCfg, "", calver, dir, opts, scratch, map[*loaderkit.UnifiedFile]bool{})
	return *scratch, nil
}

// hostBuildPrep runs the host-fs build PREP (cleanStaleBuildDirs / writeContextIgnore /
// createRemoteCandyCopies), populates the render-seam-floor renderGenCache (NewGenerator STAYS host —
// RULED), runs render-prep on that cached gen, and (real build only) ensureCharlyBinaryFresh. It
// replaces the host-fs half of the former hostBuildBuildResolve; the resolve ENVELOPE + drive-model
// are computed plugin-side.
func hostBuildPrep(_ context.Context, req spec.BuildResolveRequest, _ buildEngineContext) (map[string]string, error) {
	dir := reqDirOrCwd(req.Dir)
	boxes := req.Boxes
	gen, err := NewGenerator(dir, req.Tag, boxResolveOpts(boxes, req.IncludeDisabled))
	if err != nil {
		return nil, err
	}
	gen.DevLocalPkg = req.DevLocalPkg
	// Cache the live Generator for the render-seam host-builder (#67) — the render's host-coupled
	// seams reach the core funcs through THIS gen. One gen per dir per process.
	renderGenCache.Store(dir, gen)

	if err := gen.cleanStaleBuildDirs(); err != nil {
		return nil, fmt.Errorf("cleaning stale build dirs: %w", err)
	}
	if err := os.MkdirAll(gen.BuildDir, 0755); err != nil {
		return nil, fmt.Errorf("creating .build directory: %w", err)
	}
	if err := gen.writeContextIgnore(); err != nil {
		return nil, fmt.Errorf("writing context ignore files: %w", err)
	}
	if err := gen.createRemoteCandyCopies(); err != nil {
		return nil, fmt.Errorf("creating remote candy symlinks: %w", err)
	}
	if err := gen.toDeploykit().RenderPrepAll(); err != nil {
		return nil, err
	}
	if !req.GenerateOnly {
		if err := ensureCharlyBinaryFresh(dir, gen.Boxes, boxes); err != nil {
			return nil, fmt.Errorf("refreshing charly binary: %w", err)
		}
	}
	return map[string]string{}, nil
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

// boxResolveOpts builds the ResolveOpts that scope a generate/build to a set of
// explicitly-named boxes. It is the SINGLE source of the box-selection rule for
// both `charly box build` and `charly box generate` (R3): an empty slice means
// "all enabled boxes" (no scoping); a non-empty slice pins those names into the
// resolved set (RequestedBoxes) and, when --include-disabled is set, relaxes the
// enabled: false gate for exactly those names (IncludeDisabledNames) so the
// override never widens the working set globally. Callers pass boxes already run
// through buildkit.NormalizeBoxArgs.
func boxResolveOpts(boxes []string, includeDisabled bool) ResolveOpts {
	opts := ResolveOpts{IncludeDisabled: includeDisabled}
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

// ensureCharlyBinaryFresh rebuilds candy/charly/bin/charly when any image whose
// resolved candy chain includes the `charly` candy is in scope for the
// current build. Without this, podman build would COPY whatever stale
// binary happens to live at candy/charly/bin/charly — silently baking obsolete
// CLI behaviour into the image. Skipped (with a one-line warning) when
// `go` is not on PATH, so an end-user with a packaged charly install does
// not see a hard error. Reached from the "buildengine-prep" leg (hostBuildPrep) on
// a real build (never a generate-only pass).
func ensureCharlyBinaryFresh(dir string, boxes map[string]*buildkit.ResolvedBox, requested []string) error {
	in := requested
	if len(in) == 0 {
		in = make([]string, 0, len(boxes))
		for name := range boxes {
			in = append(in, name)
		}
	}
	needs := false
	for _, name := range in {
		img, ok := boxes[name]
		if !ok {
			continue
		}
		if slices.Contains(img.Candy, "charly") {
			needs = true
		}
		if needs {
			break
		}
	}
	if !needs {
		return nil
	}

	binPath := filepath.Join(dir, DefaultCandyDir, "charly", "bin", "charly")
	srcDir := filepath.Join(dir, "charly")

	// Downstream workspaces (project trees that `import:` upstream
	// opencharly via `@github.com/...`) don't ship the charly Go source.
	// Without ./charly to rebuild from, there's nothing to refresh — the
	// embedded candy chain will use the cached upstream binary at
	// <upstream-cache>/candy/charly/bin/charly which is already up-to-date
	// relative to upstream's charly source.
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return nil
	}

	upToDate, err := buildkit.CharlyBinaryUpToDate(binPath, srcDir)
	if err == nil && upToDate {
		return nil
	}

	if _, err := exec.LookPath("go"); err != nil {
		fmt.Fprintf(os.Stderr, "charly: warning: `go` not on PATH; skipping candy/charly/bin/charly rebuild (image will use existing binary)\n")
		return nil
	}

	fmt.Fprintf(os.Stderr, "charly: rebuilding candy/charly/bin/charly from ./charly before image build\n")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Register the buildengine-* legs at package-var init (before any init()).
var _ = func() bool {
	registerHostBuilder("buildengine-scan-local", typedHostBuilder("buildengine-scan-local", hostBuildScanLocal))
	registerHostBuilder("buildengine-collect-remote-refs", typedHostBuilder("buildengine-collect-remote-refs", hostBuildCollectRemoteRefs))
	registerHostBuilder("buildengine-ensure-repo", typedHostBuilder("buildengine-ensure-repo", hostBuildEnsureRepo))
	registerHostBuilder("buildengine-scan-remote", typedHostBuilder("buildengine-scan-remote", hostBuildScanRemote))
	registerHostBuilder("buildengine-connect-plugins", typedHostBuilder("buildengine-connect-plugins", hostBuildConnectPlugins))
	registerHostBuilder("buildengine-namespaced", typedHostBuilder("buildengine-namespaced", hostBuildNamespaced))
	registerHostBuilder("buildengine-prep", typedHostBuilder("buildengine-prep", hostBuildPrep))
	return true
}()
