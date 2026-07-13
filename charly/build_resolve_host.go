package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// build_resolve_host.go — the HOST-SIDE resolve/render SEAM behind the compiled-in
// candy/plugin-build DRIVE (P8b). P8 kept the whole box-build engine host-side
// behind HostBuild("image") — the "permanent facade"; P8b REVERSES that: the
// podman DRIVE (build loop, per-image lock, push, merge gate) moved INTO the
// candy, and the host is now a pure RESOLVE/RENDER seam provider reached via
// HostBuild("build-resolve").
//
// What STAYS host-side and rides this seam is exactly what a candy importing only
// sdk cannot do: the LOADER (NewGenerator → LoadConfig/ScanAllCandy/Validate/
// ResolveAllBox — a kernel M/B Mechanism + P6's plugin), the Containerfile RENDER
// (deploykit over the core runtime Candy graph, kept core by the P2 decision), the
// privileged builder-bootstrap (deep ResolvedBox + PacstrapDef, shared with the VM
// bootstrap path R3), and the builder-image ensure. hostBuildBuildResolve runs all
// of that and returns the SERIALIZABLE drive-model (order/levels + per-box
// descriptors + resolved tunables) the candy's podman drive consumes.
//
// generate.go / OCITarget / intermediates / layers therefore stay core PINNED BY
// the loader Mechanism + the P2 runtime-Candy decision — a documented boundary-law
// placement RE-JUDGED at P15/P16 after the loader-residue fold, never a silent keep.
//
// The build-ACTIVITY lock (retention floor) + the post-build retention prune are
// NOT here — they wrap the whole build in BuildCmd.Run host-side
// (BuildCmd.Run computes the tag once + threads it here as req.Tag so the
// activity-lock tag matches the built images). The PER-IMAGE build lock moved to
// the candy (kit.AcquireImageBuildLock) so distinct leaves still fan out in
// parallel while a shared intermediate builds cold once.

// hostBuildBuildResolve is the "build-resolve" host-builder: it reconstructs the
// project from req.Dir (NewGenerator), renders the .build/ Containerfile tree
// (gen.Generate), and — for a real build (req.GenerateOnly == false) — resolves the
// engine/platform/tunables, runs the privileged builder-bootstrap for every
// builder-based image in the build set up-front, and assembles the per-box drive
// descriptors. GenerateOnly (the `charly box generate` path) renders + returns the
// written Containerfile paths WITHOUT any build prep.
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
	if err := gen.Generate(); err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("generating build files: %w", err))}, nil
	}

	// generate-only: return the written Containerfile paths, no build prep.
	if req.GenerateOnly {
		written := make([]string, 0, len(gen.Containerfiles))
		for name := range gen.Containerfiles {
			written = append(written, filepath.Join(dir, ".build", name, "Containerfile"))
		}
		sort.Strings(written)
		return spec.BuildResolveReply{Written: written}, nil
	}

	// --- build prep (host-side; the candy drives the podman build/push/merge) ---
	def := gen.Config.Defaults
	c.resolveBuildTunables(def)

	if err := ensureCharlyBinaryFresh(dir, gen.Boxes, c.Boxes); err != nil {
		return spec.BuildResolveReply{Error: errString(fmt.Errorf("refreshing charly binary: %w", err))}, nil
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return spec.BuildResolveReply{Error: errString(err)}, nil
	}
	engine := EngineBinary(rt.BuildEngine)

	platform := c.Platform
	if platform == "" && !c.Push {
		platform = hostPlatform()
	}

	// Resolve the build order: filtered → sequential Order; full → level-parallel
	// Levels. Exactly ONE is set (mirrors buildImages' two branches).
	var order []string
	var levels [][]string
	if len(c.Boxes) > 0 {
		ord, err := ResolveBoxOrder(gen.Boxes, gen.Candies)
		if err != nil {
			return spec.BuildResolveReply{Error: errString(err)}, nil
		}
		ord, err = filterBox(ord, c.Boxes, gen.Boxes)
		if err != nil {
			return spec.BuildResolveReply{Error: errString(err)}, nil
		}
		order = ord
	} else {
		lvls, err := ResolveBoxLevels(gen.Boxes, gen.Candies)
		if err != nil {
			return spec.BuildResolveReply{Error: errString(err)}, nil
		}
		levels = lvls
	}

	// The full build set (flattened) — the images the candy will build AND the
	// images that need the privileged builder-bootstrap run up-front.
	buildSet := order
	if buildSet == nil {
		for _, level := range levels {
			buildSet = append(buildSet, level...)
		}
	}

	// Privileged builder-bootstrap for every `from: builder:` image in the set:
	// runPrivilegedBootstrap produces .build/<image>/<builder>.tar.gz the
	// Containerfile ADDs. Done host-side up-front (deep ResolvedBox + PacstrapDef +
	// ensureBuilderImageBuilt, which now recurses through dispatchBoxBuild → the
	// candy) so the candy's podman build finds every tarball; each bootstrap is
	// self-contained, so order-independent.
	for _, name := range buildSet {
		img := gen.Boxes[name]
		if img != nil && strings.HasPrefix(img.From, "builder:") {
			if err := c.runPrivilegedBootstrap(rt.BuildEngine, dir, name, img); err != nil {
				return spec.BuildResolveReply{Error: errString(fmt.Errorf("bootstrapping %s: %w", name, err))}, nil
			}
		}
	}

	// Per-box drive descriptors.
	descriptors := make([]spec.BuildResolveBox, 0, len(buildSet))
	for _, name := range buildSet {
		img := gen.Boxes[name]
		if img == nil {
			continue
		}
		d := spec.BuildResolveBox{
			Name:          name,
			FullTag:       img.FullTag,
			Containerfile: gen.Containerfiles[name],
			Registry:      img.Registry,
			Platforms:     img.Platforms,
			MergeAuto:     img.Merge != nil && img.Merge.Auto,
		}
		// Carry the per-box merge knobs (box config) — the candy's ref-based merge
		// (InvokeProvider(verb:oci)) can't re-resolve the box, so it passes these
		// through; 0 → plugin defaults (matching MergeCmd.runOne's box-config lookup).
		if img.Merge != nil {
			d.MergeMaxMB = int64(img.Merge.MaxMB)
			d.MergeMaxTotalMB = int64(img.Merge.MaxTotalMB)
		}
		descriptors = append(descriptors, d)
	}

	return spec.BuildResolveReply{
		Engine:     engine,
		EngineName: rt.BuildEngine,
		Platform:   platform,
		Order:      order,
		Levels:     levels,
		Boxes:      descriptors,
		Jobs:       int64(resolveBuildJobs(c)),
		PodmanJobs: int64(resolvePodmanJobs(c.PodmanJobs, c.podmanJobsCap)),
		Cache:      c.Cache,
		KeepImages: int64(resolveIntPtr(def.KeepImages, nil, keepImagesFallback)),
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

// Register the build-resolve host-builder at package-var init (before any init(),
// like the image/containerfiles/overlay builders it replaces on the CLI path).
// "build-resolve" is a CLASS-GENERIC action noun (mirrors config-resolve/vm-build,
// never a provider word — the F11 uniform-API gate).
var _ = func() bool {
	registerHostBuilder("build-resolve", typedHostBuilder("build-resolve", hostBuildBuildResolve))
	return true
}()
