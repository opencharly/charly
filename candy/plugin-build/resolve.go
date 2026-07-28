package build

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"
)

// resolve.go — the PLUGIN-SIDE build-engine RESOLVE (K3 build-engine, U6 — the bootstrap-critical
// inversion). candy/plugin-build runs the whole NewGenerator+build-prep RESOLVE ITSELF, over the K1
// loader reverse legs + a small `buildengine-*` host-leg family, reaching the host ONLY for what a
// sdk-only candy structurally cannot do:
//
//   - the config LOAD               → loaderkit.LoadUnified(dir, LoadSeamsFromExecutor(ex))  [K1, landed]
//   - the local candy SCAN          → HostBuild("buildengine-scan-local")  (bootstrap-delicate
//                                     parseCandyYAML→buildCandy; the B bootstrap root STAYS core)
//   - the remote candy FETCH fixpt  → loaderkit.ScanCandyFromLocal(seams) over three thin legs
//   - the build-time plugin CONNECT → HostBuild("buildengine-connect-plugins")  (registry M)
//   - the pre-build VALIDATE gate    → InvokeProvider(command:validate)  (plugin↔plugin)
//   - the `resource:` kind resolve   → InvokeProvider(kind:resource)  (plugin↔plugin, via loaderkit)
//   - the distro/init vocab resolve  → InvokeProvider(kind:distro|init)  (plugin↔plugin, via loaderkit)
//   - the host-fs PREP + user probe  → HostBuild("buildengine-prep")  (cleanStaleBuildDirs /
//                                     writeContextIgnore / createRemoteCandyCopies / resolveUserContext /
//                                     ensureCharlyBinaryFresh + the render-seam-floor renderGenCache;
//                                     NewGenerator STAYS host for the render-seam floor — RULED)
//
// Everything else — buildkit.ResolveAllBox / deploykit.ComputeIntermediates / GlobalCandyOrder /
// ComputeEffectiveVersions / RenderPrepAll / ResolveBoxOrder / ResolveBoxLevels / the drive-model
// (engine/platform/descriptors/tunables) / loaderkit.ProjectResolvedProject — is PURE sdk the plugin
// runs directly, so the resolve ORCHESTRATION + the drive-model computation leave charly core. The
// render DRIVE (render.go) already reads the envelope this produces (#67). This REPLACES the host
// build-prep fat seam (charly/build_resolve_host.go's hostBuildBuildResolve), which is DELETED.

// resolveBuildEngine runs the build-engine RESOLVE plugin-side and returns the same
// spec.BuildResolveReply (envelope + drive-model) the former host build-prep seam produced. Shared by
// runBoxBuild (generateOnly=false) + runBoxGenerate (generateOnly=true) — the ONLY difference is the
// generate path returns after the envelope (no engine/order drive-model).
//
// validate, resolve, intermediates, render-prep, order, user-context, envelope, drive-model) run
// plugin-side; one branch per step, mirroring the former host hostBuildBuildResolve.
//
//nolint:gocyclo // resolve orchestrator — the linear NewGenerator sequence (load, vocab, scan, connect,
func resolveBuildEngine(ctx context.Context, ex *sdk.Executor, req spec.BuildRequest, generateOnly bool) (spec.BuildResolveReply, error) {
	dir := req.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return spec.BuildResolveReply{}, err
		}
		dir = cwd
	}
	boxes := buildkit.NormalizeBoxArgs(req.Boxes)

	rr := spec.ResolvedProjectRequest{Dir: dir, IncludeDisabled: req.IncludeDisabled, ExtraCandyRefs: nil}

	// --- 1. LOAD the project plugin-side (K1 reverse legs) ---
	exec := &buildLoaderExecutor{ctx: ctx, ex: ex}
	uf, ok, err := loaderkit.LoadUnified(dir, loaderkit.LoadSeamsFromExecutor(exec))
	if err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}
	if !ok || uf == nil {
		// No project (project-less dir) — empty resolve (mirrors the empty-project contract).
		return spec.BuildResolveReply{ResolvedProject: &spec.ResolvedProject{}}, nil
	}
	cfg := uf.ProjectConfig()

	// --- 2. build VOCABULARY (distro/builder/init) — plugin-side projection over uf ---
	distroCfg := loaderkit.ProjectDistroConfig(uf, resolveDistroLeg(ctx, ex))
	builderCfg := loaderkit.ProjectBuilderConfig(uf)
	initCfg := loaderkit.ProjectInitConfig(uf, resolveInitLeg(ctx, ex))

	// --- 3. SCAN candies: local (host leg) + remote fetch fixpoint (loaderkit + seam legs) ---
	localScanned, err := scanLocalLeg(ctx, ex, rr)
	if err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}
	layers, err := loaderkit.ScanCandyFromLocal(localScanned, initCfg, scanSeamsLeg(ctx, ex, rr))
	if err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}

	// --- 4. build-time plugin CONNECT (registry M — host leg) ---
	if err := hostVoidLeg(ctx, ex, "buildengine-connect-plugins", rr); err != nil {
		// Best-effort, mirroring NewGenerator: a connect failure warns; a plugin the build actually
		// USES fails loudly later at OpEmit/OpResolve.
		fmt.Fprintf(os.Stderr, "warning: build-time plugin load: %v\n", err)
	}

	// --- 5. pre-build VALIDATE gate (plugin↔plugin) ---
	if err := validateProjectLeg(ctx, ex, rr); err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}

	// --- 6. RESOLVE boxes (pure sdk) ---
	// Stamp the build tag ONCE plugin-side when the host leaves it empty (bare `charly box generate`,
	// the builder-bootstrap re-dispatch), then thread it to the prep + namespaced legs so the host's
	// NewGenerator uses the SAME tag (ComputeCalVer is clock-derived — computing it twice would diverge).
	tag := req.Tag
	if tag == "" {
		tag = buildkit.ComputeCalVer()
	}
	ropts := boxBuildkitOpts(boxes, req.IncludeDisabled, distroCfg, builderCfg)
	resolved, err := buildkit.ResolveAllBox(cfg, tag, dir, ropts)
	if err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}
	// auto-intermediates + global candy order + effective versions (pure sdk).
	resolved, err = deploykit.ComputeIntermediates(resolved, layers, intermediateDefaults(cfg), tag)
	if err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("computing intermediates: %w", err))}, nil
	}
	globalOrder, err := deploykit.GlobalCandyOrder(resolved, layers)
	if err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("computing global candy order: %w", err))}, nil
	}
	if err := deploykit.ComputeEffectiveVersions(resolved, layers); err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}

	// --- 7. host-fs PREP + renderGenCache + ensureCharlyBinaryFresh (host leg) ---
	// The host rebuilds NewGenerator (the render-seam-floor gen it STAYS host for), runs the host-fs
	// prep (cleanStaleBuildDirs / writeContextIgnore / createRemoteCandyCopies) + populates the
	// renderGenCache the render-seam floor reuses; ensureCharlyBinaryFresh runs there too (real build
	// only, never for generate). resolveUserContext is reproduced plugin-side below (step 9).
	if err := prepLeg(ctx, ex, spec.BuildResolveRequest{
		Boxes: boxes, Tag: tag, Dir: dir, IncludeDisabled: req.IncludeDisabled,
		GenerateOnly: generateOnly,
	}); err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}

	// --- 8. RENDER-PREP (pure deploykit.Generator method) fills the per-box build-render caches ---
	dg := deploykit.NewRenderGenerator()
	dg.Dir = dir
	dg.Tag = tag
	dg.Config = cfg
	dg.Candies = layers
	dg.Boxes = resolved
	dg.InitConfig = initCfg
	dg.GlobalOrder = globalOrder
	dg.DevLocalPkg = req.DevLocalPkg
	if err := dg.RenderPrepAll(); err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}

	// --- 9. build ORDER (+ user-context applied post-render-prep, mirroring the host order) ---
	order, err := deploykit.ResolveBoxOrder(resolved, layers)
	if err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("resolving box order: %w", err))}, nil
	}
	if len(boxes) > 0 {
		order, err = deploykit.FilterBox(order, boxes, resolved)
		if err != nil {
			return spec.BuildResolveReply{Error: errString(fmt.Errorf("scoping generation to requested boxes: %w", err))}, nil
		}
	}
	// resolveUserContext, reproduced plugin-side (post-render-prep, per-box in order — the SAME order
	// the former host hostBuildBuildResolve used). External-base user probe rides InvokeProvider(verb:oci).
	for _, name := range order {
		resolveUserContextPlugin(ctx, ex, cfg, resolved, resolved[name])
	}

	// --- 10. project the resolved-project ENVELOPE (pure loaderkit assembler + plugin seams) ---
	rp, err := projectResolvedProjectLeg(ctx, ex, cfg, layers, uf, distroCfg, builderCfg, initCfg, dir, uf.Version, tag, req.IncludeDisabled, resolved)
	if err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("projecting resolved-project envelope: %w", err))}, nil
	}
	rp.GlobalOrder = globalOrder

	// generate-only: return the envelope + order so render.go can render (no podman).
	if generateOnly {
		return spec.BuildResolveReply{ResolvedProject: rp, Order: order}, nil
	}

	// --- 11. DRIVE-MODEL (pure): engine/platform/levels/descriptors/tunables ---
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}
	engine := kit.EngineBinary(rt.BuildEngine)
	platform := req.Platform
	if platform == "" && !req.Push {
		platform = buildkit.HostPlatform()
	}
	var levels [][]string
	if len(boxes) == 0 {
		levels, err = deploykit.ResolveBoxLevels(resolved, layers)
		if err != nil {
			return spec.BuildResolveReply{Error: errString(err)}, nil
		}
	}
	buildSet := order
	if buildSet == nil {
		for _, level := range levels {
			buildSet = append(buildSet, level...)
		}
	}
	descriptors := buildDriveDescriptors(buildSet, resolved)

	def := cfg.Defaults
	return spec.BuildResolveReply{
		Engine:          engine,
		EngineName:      rt.BuildEngine,
		Platform:        platform,
		Order:           order,
		Levels:          levels,
		Boxes:           descriptors,
		Jobs:            int64(resolveDriveJobs(int(req.Jobs), def)),
		PodmanJobs:      int64(buildkit.ResolvePodmanJobs(resolveDrivePodmanJobs(int(req.PodmanJobs), def), resolveIntPtrDrive(def.PodmanJobsCap))),
		Cache:           resolveDriveCache(req.Cache, def),
		KeepImages:      int64(resolveIntPtrDrive(def.KeepImages)),
		ResolvedProject: rp,
	}, nil
}

// buildDriveDescriptors builds the per-box drive descriptors (NO Containerfile content — the drive
// renders them) for every box in the build set, mirroring the former host hostBuildBuildResolve loop.
func buildDriveDescriptors(buildSet []string, resolved map[string]*buildkit.ResolvedBox) []spec.BuildResolveBox {
	descriptors := make([]spec.BuildResolveBox, 0, len(buildSet))
	for _, name := range buildSet {
		img := resolved[name]
		if img == nil {
			continue
		}
		d := spec.BuildResolveBox{
			Name:      name,
			FullTag:   img.FullTag,
			Registry:  img.Registry,
			Platforms: img.Platforms,
			MergeAuto: img.Merge != nil && img.Merge.Auto,
		}
		if img.Merge != nil {
			d.MergeMaxMB = int64(img.Merge.MaxMB)
			d.MergeMaxTotalMB = int64(img.Merge.MaxTotalMB)
		}
		if strings.HasPrefix(img.From, "builder:") {
			d.From = img.From
			d.BootstrapBuilderImage = img.BootstrapBuilderImage
			d.DistroDef = img.DistroDef
			builderName := strings.TrimPrefix(img.From, "builder:")
			if img.BuilderConfig != nil {
				d.BootstrapBuilder = img.BuilderConfig.Builder[builderName]
			}
		}
		descriptors = append(descriptors, d)
	}
	return descriptors
}

// --- pure drive-model tunable resolvers (mirror BuildCmd.resolveBuildTunables / resolveBuildJobs) ---

func resolveDriveJobs(jobs int, def spec.BoxConfig) int {
	if jobs == 0 {
		jobs = resolveIntPtrDrive(def.Jobs)
	}
	if jobs < 1 {
		return jobsFallback
	}
	return jobs
}

func resolveDrivePodmanJobs(pj int, def spec.BoxConfig) int {
	if pj == 0 {
		return resolveIntPtrDrive(def.PodmanJobs)
	}
	return pj
}

func resolveDriveCache(cache string, def spec.BoxConfig) string {
	if cache == "" {
		return def.Cache
	}
	return cache
}

func resolveIntPtrDrive(v *int) int {
	if v != nil {
		return *v
	}
	return 0
}

// boxBuildkitOpts mirrors charly's boxResolveOpts projected onto buildkit.ResolveOpts (the pure
// resolver's opts), threading the already-projected DistroCfg/BuilderCfg so ResolveAllBox never
// reloads the vocabulary.
func boxBuildkitOpts(boxes []string, includeDisabled bool, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig) buildkit.ResolveOpts {
	o := buildkit.ResolveOpts{IncludeDisabled: includeDisabled, DistroCfg: distroCfg, BuilderCfg: builderCfg}
	if len(boxes) > 0 {
		o.RequestedBoxes = boxes
		if includeDisabled {
			o.IncludeDisabledNames = make(map[string]bool, len(boxes))
			for _, name := range boxes {
				o.IncludeDisabledNames[name] = true
			}
		}
	}
	return o
}

// intermediateDefaults lifts cfg.Defaults into deploykit.IntermediateDefaults (mirrors charly's
// ComputeIntermediates shim).
func intermediateDefaults(cfg *spec.Config) deploykit.IntermediateDefaults {
	return deploykit.IntermediateDefaults{
		Builder:   buildkit.BuilderMap(cfg.Defaults.Builder),
		UID:       cfg.Defaults.UID,
		User:      cfg.Defaults.User,
		GID:       cfg.Defaults.GID,
		Merge:     cfg.Defaults.Merge,
		Registry:  cfg.Defaults.Registry,
		Platforms: cfg.Defaults.Platforms,
		Distro:    cfg.Defaults.Distro,
		Build:     cfg.Defaults.Build,
	}
}

// resolveUserContextPlugin is the plugin-side reproduction of charly's Generator.resolveUserContext
// (verbatim logic): an internal-base box inherits its parent's user (respecting explicit overrides);
// an external-base box probes the base image for a uid-1000 account via InvokeProvider(verb:oci).
// It mutates img in place; a nil img (a name filtered out of the resolved set) is a no-op.
func resolveUserContextPlugin(ctx context.Context, ex *sdk.Executor, cfg *spec.Config, boxes map[string]*buildkit.ResolvedBox, img *buildkit.ResolvedBox) {
	if img == nil {
		return
	}
	if !img.IsExternalBase {
		parentImg := boxes[img.Base]
		origCfg, _ := cfg.BoxConfig(img.Name)
		if parentImg != nil {
			if origCfg.User == "" {
				img.User = parentImg.User
			}
			if origCfg.UID == nil {
				img.UID = parentImg.UID
			}
			if origCfg.GID == nil {
				img.GID = parentImg.GID
			}
		}
		switch {
		case img.User == "root":
			img.Home = "/root"
		case origCfg.User == "" && origCfg.UID == nil && parentImg != nil:
			img.Home = parentImg.Home
		default:
			img.Home = fmt.Sprintf("/home/%s", img.User)
		}
		return
	}
	// External base — probe the base image for the configured uid via verb:oci (inspect-user).
	info, err := inspectUserLeg(ctx, ex, img.Base, img.UID)
	if err != nil {
		return // can't inspect — keep configured defaults
	}
	if info.Found {
		img.User = info.Name
		img.Home = info.Home
		img.GID = info.GID
	}
}
