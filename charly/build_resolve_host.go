package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// build_resolve_host.go — the HOST-SIDE build-prep SEAM behind the compiled-in
// candy/plugin-build DRIVE (#67 render-DRIVE move). The podman DRIVE (build loop,
// per-image lock, push, merge gate) lives in candy/plugin-build; the host is a
// pure PREP + RESOLVE-PROJECT envelope provider reached via HostBuild("build-prep").
//
// What STAYS host-side and rides this seam is exactly what a candy importing only
// sdk cannot do: the LOADER (NewGenerator → LoadConfig/ScanAllCandy/Validate/
// ResolveAllBox — a kernel M/B Mechanism), the build PREP (cleanStaleBuildDirs,
// MkdirAll, writeContextIgnore, createRemoteCandyCopies — host fs operations), the
// render-prep (fill build-render caches on ResolvedBox by reading the live graph),
// the resolved-project ENVELOPE projection (with caches), the privileged
// builder-bootstrap, and the builder-image ensure. The Containerfile RENDER itself
// moved to deploykit.Generator (sdk/deploykit/generate.go) — plugin-build builds
// the Generator from the envelope + runs Generate.
//
// The build-ACTIVITY lock (retention floor) + the post-build retention prune are
// NOT here — they wrap the whole build in BuildCmd.Run host-side.

// hostBuildBuildResolve is the "build-prep" host-builder: it reconstructs the
// project from req.Dir (NewGenerator), runs the build prep (cleanStaleBuildDirs,
// MkdirAll, writeContextIgnore, createRemoteCandyCopies), runs render-prep (fills
// build-render caches on ResolvedBox), resolves the build order, and projects the
// resolved-project envelope (with caches). The reply carries the envelope + the
// drive-model (order/levels + per-box descriptors + resolved tunables). The
// Containerfile RENDER is NOT done here — plugin-build renders via deploykit.Generator.
// For GenerateOnly, plugin-build renders + writes the Containerfiles + returns the
// paths; for a real build, plugin-build renders + pipes to podman.
//
//nolint:gocyclo // build-prep orchestrator — the linear prep sequence (fs prep, render-prep, order, envelope projection, build-prep) over the build-prep seam; one branch per prep step.
func hostBuildBuildResolve(_ context.Context, req spec.BuildResolveRequest, _ buildEngineContext) (spec.BuildResolveReply, error) {
	dir := req.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return spec.BuildResolveReply{}, err
		}
		dir = cwd
	}

	boxes := normalizeBoxArgs(req.Boxes)
	c := &BuildCmd{
		Boxes:           boxes,
		Tag:             req.Tag,
		Push:            req.Push,
		Platform:        req.Platform,
		Cache:           req.Cache,
		NoCache:         req.NoCache,
		Jobs:            int(req.Jobs),
		PodmanJobs:      int(req.PodmanJobs),
		IncludeDisabled: req.IncludeDisabled,
		DevLocalPkg:     req.DevLocalPkg,
	}

	gen, err := NewGenerator(dir, c.Tag, boxResolveOpts(boxes, c.IncludeDisabled))
	if err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}
	gen.DevLocalPkg = c.DevLocalPkg
	// Cache the live Generator for the render-seam host-builder (#67): plugin-build's
	// deploykit.Generator render calls back via HostBuild("render-seam") for each
	// host-coupled seam (RenderService, builder resolves, ValidateEgress, …); those
	// reach the core funcs through THIS gen (gen.Boxes/gen.Candies/gen.Config/gen.Dir).
	// One gen per dir per process — build-prep is the first HostBuild, so the cache is
	// populated before any render-seam call.
	renderGenCache.Store(dir, gen)

	// --- build prep (host-side fs operations; plugin-build does NOT do these) ---
	if err := gen.cleanStaleBuildDirs(); err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("cleaning stale build dirs: %w", err))}, nil
	}
	if err := os.MkdirAll(gen.BuildDir, 0755); err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("creating .build directory: %w", err))}, nil
	}
	if err := gen.writeContextIgnore(); err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("writing context ignore files: %w", err))}, nil
	}
	if err := gen.createRemoteCandyCopies(); err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("creating remote candy symlinks: %w", err))}, nil
	}

	// --- render-prep: fill build-render caches on every box ---
	if err := renderPrepAll(gen); err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}

	// --- resolve user context (in order, parents first) ---
	order, err := deploykit.ResolveBoxOrder(gen.Boxes, gen.Candies)
	if err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("resolving box order: %w", err))}, nil
	}
	if len(gen.RequestedBoxes) > 0 {
		order, err = filterBox(order, gen.RequestedBoxes, gen.Boxes)
		if err != nil {
			return spec.BuildResolveReply{Error: errString(fmt.Errorf("scoping generation to requested boxes: %w", err))}, nil
		}
	}
	for _, name := range order {
		gen.resolveUserContext(gen.Boxes[name])
	}

	// --- project the resolved-project envelope (with build-render caches) ---
	// Load the project via loadProjectForResolve to get the uf (deploy tree, templates,
	// agent bodies) + the build-vocab configs the projector needs. Use the Generator's
	// pre-resolved boxes (with caches) as the preResolvedBoxes so the caches survive.
	lp, err := loadProjectForResolve(dir, boxResolveOpts(boxes, c.IncludeDisabled), nil)
	if err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("loading project for envelope: %w", err))}, nil
	}
	var rp *spec.ResolvedProject
	if !lp.empty {
		rp, err = projectResolvedProjectWithBoxes(lp.cfg, lp.layers, lp.uf, lp.distroCfg, lp.builderCfg, gen.InitConfig, dir, lp.version, boxResolveOpts(boxes, c.IncludeDisabled), nil, gen.Boxes)
		if err != nil {
			return spec.BuildResolveReply{Error: errString(fmt.Errorf("projecting resolved-project envelope: %w", err))}, nil
		}
		// Fill the build-render-only project-level fields (#67).
		rp.GlobalOrder = gen.GlobalOrder
		rp.ExternalizedBuilders = externalizedBuilders
	} else {
		rp = &spec.ResolvedProject{}
	}

	// generate-only: return the envelope + order so plugin-build can render.
	if req.GenerateOnly {
		return spec.BuildResolveReply{
			ResolvedProject: rp,
			Order:           order,
		}, nil
	}

	// --- build prep (host-side; the candy drives the podman build/push/merge) ---
	def := gen.Config.Defaults
	c.resolveBuildTunables(def)

	if err := ensureCharlyBinaryFresh(dir, gen.Boxes, c.Boxes); err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("refreshing charly binary: %w", err))}, nil
	}

	rt, err := kit.ResolveRuntime()
	if err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}
	engine := kit.EngineBinary(rt.BuildEngine)

	platform := c.Platform
	if platform == "" && !c.Push {
		platform = hostPlatform()
	}

	// Resolve the build order: filtered → sequential Order; full → level-parallel
	// Levels. Exactly ONE is set (mirrors buildImages' two branches).
	var levels [][]string
	if len(c.Boxes) > 0 {
		// order already resolved above
	} else {
		levels, err = deploykit.ResolveBoxLevels(gen.Boxes, gen.Candies)
		if err != nil {
			return spec.BuildResolveReply{Error: errString(err)}, nil
		}
	}

	// The full build set (flattened) — the images the candy will build AND the
	// images that need the privileged builder-bootstrap run up-front.
	buildSet := order
	if buildSet == nil {
		for _, level := range levels {
			buildSet = append(buildSet, level...)
		}
	}

	// Privileged builder-bootstrap for every `from: builder:` image in the set.
	for _, name := range buildSet {
		img := gen.Boxes[name]
		if img != nil && strings.HasPrefix(img.From, "builder:") {
			if err := c.runPrivilegedBootstrap(rt.BuildEngine, dir, name, img); err != nil {
				return spec.BuildResolveReply{Error: errString(fmt.Errorf("bootstrapping %s: %w", name, err))}, nil
			}
		}
	}

	// Per-box drive descriptors (NO Containerfile content — plugin-build renders).
	descriptors := make([]spec.BuildResolveBox, 0, len(buildSet))
	for _, name := range buildSet {
		img := gen.Boxes[name]
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
		descriptors = append(descriptors, d)
	}

	return spec.BuildResolveReply{
		Engine:          engine,
		EngineName:      rt.BuildEngine,
		Platform:        platform,
		Order:           order,
		Levels:          levels,
		Boxes:           descriptors,
		Jobs:            int64(resolveBuildJobs(c)),
		PodmanJobs:      int64(resolvePodmanJobs(c.PodmanJobs, c.podmanJobsCap)),
		Cache:           c.Cache,
		KeepImages:      int64(resolveIntPtr(def.KeepImages, nil, keepImagesFallback)),
		ResolvedProject: rp,
	}, nil
}

// resolveBuildJobs returns the outer image-level concurrency (images per DAG
// level), applying the same jobsFallback the drive loop used.
func resolveBuildJobs(c *BuildCmd) int {
	if c.Jobs < 1 {
		return jobsFallback
	}
	return c.Jobs
}

// Register the build-prep host-builder at package-var init (before any init(),
// like the image/containerfiles/overlay builders it replaces on the CLI path).
// "build-prep" is a CLASS-GENERIC action noun (mirrors config-resolve/vm-build,
// never a provider word — the F11 uniform-API gate).
var _ = func() bool {
	registerHostBuilder("build-prep", typedHostBuilder("build-prep", hostBuildBuildResolve))
	return true
}()
