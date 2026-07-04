package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// Generator holds state for generating build artifacts
type Generator struct {
	Dir            string
	Config         *Config
	Candies        map[string]*Candy
	Tag            string
	Boxes          map[string]*ResolvedBox
	BuildDir       string
	Containerfiles map[string]string // cached content per image (used by charly build to pipe via stdin)
	GlobalOrder    []string          // popularity-weighted global candy order for cache optimization

	// RequestedBoxes scopes which Containerfiles Generate() writes: when
	// non-empty, only the named boxes and their transitive deps (Base + format
	// builders + bootstrap builder) are emitted — the SAME filterBox set the
	// build path uses to scope `podman build` (R3, build/generate unified). Empty
	// means "every enabled box" (the bare `charly box generate` / `generate all`
	// and full `charly box build` behaviour). The whole resolved graph
	// (intermediates, global candy order, effective versions) is still computed
	// in NewGenerator regardless — only the per-box emission loop is scoped.
	RequestedBoxes []string

	// DevLocalPkg, when true, makes localpkg candies (the charly toolchain) build
	// from LOCAL in-development source instead of downloading the published
	// release. Set ONLY for disposable check-bed image builds (the check-bed runner
	// passes `--dev-local-pkg`), so a bed always tests the in-development charly;
	// a production box build leaves it false. See renderLocalPkgImageInstall.
	DevLocalPkg bool

	// externalBuilderReplies caches each candy's external-builder OpResolve reply
	// for ONE image: emitExternalBuilderStages populates it (writing the pre-main-FROM
	// stage) and emitExternalBuilderArtifacts reads it (writing the post-main-FROM
	// COPY --from artifacts) — so the provider is Invoked exactly once per candy. Keyed
	// by candy name; RESET per image at the start of emitExternalBuilderStages.
	externalBuilderReplies map[string]spec.BuilderResolveReply

	// detectionBuilderReplies caches each (candy, builder) DETECTION-builder OpResolve
	// reply for ONE image: emitBuilderStages populates it (writing the pre-main-FROM
	// FROM…AS stage) and emitBuilderArtifacts reads it (writing the post-main-FROM
	// COPY --from artifacts + the once-per-builder binary copy) — so the builder plugin
	// is Invoked exactly once per (candy, builder). Keyed by builderCtxKey(candy, builder);
	// RESET per image at the start of emitBuilderStages. This is the C10 detection-builder
	// (pixi/npm/aur) counterpart of externalBuilderReplies for the `external_builder:` path.
	detectionBuilderReplies map[string]spec.BuilderResolveReply
}

// globalOrderForBox returns the candy order for an image by filtering the
// global order to only include the image's needed candies. This ensures shared
// candies appear in the same order across all images, maximizing cache reuse.
func (g *Generator) globalOrderForBox(imageCandies []string, parentCandies map[string]bool) ([]string, error) {
	// Resolve needed candies (expand composition + transitive deps)
	needed, err := ResolveCandyOrder(imageCandies, g.Candies, parentCandies)
	if err != nil {
		return nil, err
	}

	neededSet := make(map[string]bool, len(needed))
	for _, l := range needed {
		neededSet[l] = true
	}

	// Filter global order to only include this image's needed candies
	var order []string
	for _, l := range g.GlobalOrder {
		if neededSet[l] {
			order = append(order, l)
		}
	}

	// Safety: if global order is missing some needed candies (shouldn't happen),
	// append them in their original order
	for _, l := range needed {
		found := slices.Contains(order, l)
		if !found {
			order = append(order, l)
		}
	}

	return order, nil
}

// resolveUserContext detects existing user in base image or uses configured values
func (g *Generator) resolveUserContext(img *ResolvedBox) {
	if !img.IsExternalBase {
		// Internal base - inherit from parent, but respect explicit overrides
		parentImg := g.Boxes[img.Base]
		origCfg := g.Config.Box[img.Name]

		if origCfg.User == "" {
			img.User = parentImg.User
		}
		if origCfg.UID == nil {
			img.UID = parentImg.UID
		}
		if origCfg.GID == nil {
			img.GID = parentImg.GID
		}

		// Resolve home directory
		switch {
		case img.User == "root":
			img.Home = "/root"
		case origCfg.User == "" && origCfg.UID == nil:
			img.Home = parentImg.Home
		default:
			img.Home = fmt.Sprintf("/home/%s", img.User)
		}
		return
	}

	// External base - try to detect existing user at configured UID
	userInfo, err := InspectImageUser(img.Base, img.UID)
	if err != nil {
		// Can't inspect, use configured defaults
		return
	}

	if userInfo != nil {
		// Found existing user - use their info
		img.User = userInfo.Name
		img.Home = userInfo.Home
		img.GID = userInfo.GID
	}
	// else: no user found at UID, will create with configured values
}

// NewGenerator creates a new generator. opts is propagated through Validate
// + ResolveAllBox so `charly box build --include-disabled` reaches images
// flagged enabled: false in charly.yml (without modifying the file).
func NewGenerator(dir string, tag string, opts ResolveOpts) (*Generator, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}

	// Load default build config early — needed for RegisterBuildVocabulary before candy scanning.
	// Post-unified-cutover this reads charly.yml directly (no format_config: pointer).
	defaultDistroCfg, _, defaultInitCfg, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		return nil, fmt.Errorf("loading default build config: %w", err)
	}
	RegisterBuildVocabulary(defaultDistroCfg)

	layers, err := ScanAllCandyWithConfigOpts(dir, cfg, opts)
	if err != nil {
		return nil, err
	}

	// Build-time plugin connect (operator-authorized build-time plugin execution).
	// Connect the project's OUT-OF-TREE plugin candies so an external step/builder/verb
	// provider is registered + dialable DURING image generation — the SAME loader the
	// deploy/check paths use (loadDeployPlugins / attachCheckRunnerContext), transport-
	// invisible above the registry. A BUILTIN plugin is already registered via init() and
	// needs no connect; only an EXTERNAL one is host-built + connected here. This is what
	// lets a `run:` plugin verb (and a plugin builder) EXECUTE at build time to emit its
	// Containerfile fragment, placement-agnostically: in-proc for a builtin, over go-plugin
	// gRPC for an external. Best-effort: a connect failure on a plugin the build actually
	// USES fails loudly at emit (emitTasks' OpEmit dispatch), never silently mis-builds.
	// PERF-SCOPED: connect ONLY the plugins the candy plans (run-step verbs) + candy
	// external_builder selections + box plans reference — an unreferenced box/<distro>
	// plugin candy is not host-built. No deploy substrate / add_candy at build (no deploy).
	// A detection-builder's build-time multi-stage is resolved by its plugin's OpResolve leg
	// (C10, kit.BuilderResolve); emitBuilderStages connects those builder plugins on-demand
	// (ensureBuildersConnected), so they are NOT collected here.
	buildRefs := collectReferencedPluginWords(layers, cfg.Box, nil)
	if perr := loadProjectPlugins(context.Background(), layers, buildRefs); perr != nil {
		fmt.Fprintf(os.Stderr, "warning: build-time plugin load: %v\n", perr)
	}

	// Populate init systems on candies from the embedded build vocabulary
	PopulateCandyInitSystem(layers, defaultInitCfg)

	if err := Validate(cfg, layers, dir, opts); err != nil {
		return nil, err
	}

	// Compute CalVer if tag not specified
	if tag == "" {
		tag = ComputeCalVer()
	}

	images, err := cfg.ResolveAllBox(tag, dir, opts)
	if err != nil {
		return nil, err
	}

	// Compute and inject auto-intermediate images
	updated, err := ComputeIntermediates(images, layers, cfg, tag)
	if err != nil {
		return nil, fmt.Errorf("computing intermediates: %w", err)
	}
	images = updated

	// Compute global candy order for consistent cross-image ordering
	globalOrder, err := GlobalCandyOrder(images, layers)
	if err != nil {
		return nil, fmt.Errorf("computing global candy order: %w", err)
	}

	g := &Generator{
		Dir:            dir,
		Config:         cfg,
		Candies:        layers,
		Tag:            tag,
		Boxes:          images,
		BuildDir:       filepath.Join(dir, ".build"),
		Containerfiles: make(map[string]string),
		GlobalOrder:    globalOrder,
		RequestedBoxes: opts.RequestedBoxes,
	}

	// Derive each image's content-stable identity (ai.opencharly.version)
	// from per-entity versions now that the base chain + auto-intermediates are
	// materialized. See effective_version.go.
	if err := g.computeEffectiveVersions(); err != nil {
		return nil, err
	}

	return g, nil
}

// cleanStaleBuildDirs removes image directories in .build/ that don't correspond
// to any enabled image, and removes leftover files like docker-bake.hcl.
func (g *Generator) cleanStaleBuildDirs() error {
	entries, err := os.ReadDir(g.BuildDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			// Skip charly-managed staging dirs (_candy, _buildconfig, .locks,
			// transient ._*.tmp.* dirs): they are NOT images, and removing them
			// races a concurrent build that is COPYing from / locking on them.
			if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
				continue
			}
			if _, exists := g.Boxes[name]; !exists {
				path := filepath.Join(g.BuildDir, name)
				if err := os.RemoveAll(path); err != nil {
					return fmt.Errorf("removing stale dir %s: %w", path, err)
				}
				fmt.Fprintf(os.Stderr, "Removed stale build dir: .build/%s\n", name)
			}
		} else if entry.Name() == "docker-bake.hcl" {
			// Remove leftover HCL file from pre-charly-build era
			path := filepath.Join(g.BuildDir, entry.Name())
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("removing stale file %s: %w", path, err)
			}
			fmt.Fprintf(os.Stderr, "Removed stale file: .build/%s\n", entry.Name())
		}
	}
	return nil
}

// Generate generates all build artifacts
func (g *Generator) Generate() error {
	// Clean stale image directories from .build/ (leftovers from removed/renamed images)
	if err := g.cleanStaleBuildDirs(); err != nil {
		return fmt.Errorf("cleaning stale build dirs: %w", err)
	}

	// Create .build directory
	if err := os.MkdirAll(g.BuildDir, 0755); err != nil {
		return fmt.Errorf("creating .build directory: %w", err)
	}

	// Render the build-context ignore files (.containerignore + .dockerignore)
	// from defaults.context_ignore. The context tar streams to the build
	// engine on EVERY build regardless of cache state, so excluding heavy
	// never-COPYed directories is the dominant warm-rebuild win.
	if err := g.writeContextIgnore(); err != nil {
		return fmt.Errorf("writing context ignore files: %w", err)
	}

	// Stage remote candies into versioned .build/_candy/<name>.<version>/ dirs
	if err := g.createRemoteCandyCopies(); err != nil {
		return fmt.Errorf("creating remote candy symlinks: %w", err)
	}

	// Resolve box build order
	order, err := ResolveBoxOrder(g.Boxes, g.Candies)
	if err != nil {
		return fmt.Errorf("resolving box order: %w", err)
	}

	// Scope the emission to the requested boxes + their transitive deps, using
	// the SAME filterBox the build path uses to scope `podman build` (R3:
	// `charly box generate <name>` and `charly box build <name>` select identically).
	// Empty RequestedBoxes leaves the full enabled set — the bare-generate /
	// `generate all` / full-build behaviour. Transitive deps stay in `order` so a
	// requested child's Base/builders are emitted too, and dependency order
	// (parents first) is preserved.
	if len(g.RequestedBoxes) > 0 {
		order, err = filterBox(order, g.RequestedBoxes, g.Boxes)
		if err != nil {
			return fmt.Errorf("scoping generation to requested boxes: %w", err)
		}
	}

	// Resolve user context for each image (in order, so parents are resolved first)
	for _, name := range order {
		g.resolveUserContext(g.Boxes[name])
	}

	// Generate Containerfile for each image
	for _, name := range order {
		if err := g.generateContainerfile(name); err != nil {
			return fmt.Errorf("generating Containerfile for %s: %w", name, err)
		}
	}

	return nil
}

// baselineContextIgnore is the always-excluded set written into the generated
// .containerignore / .dockerignore regardless of project config. It combines
// the universal VCS/binary excludes (formerly the static .containerignore)
// with the cache-hygiene globs (formerly the static .dockerignore) that keep
// editor and Python/Node cruft from busting the build cache. Project-specific
// heavy directories are layered on top via defaults.context_ignore so a
// third-party project with no context_ignore still gets sane defaults.
// baselineContextIgnore is the built-in build-context ignore baseline, read from the
// context_ignore_baseline directive in the embedded charly.yml (Phase 4: data moved out of
// Go) rather than hardcoded here. A project's defaults.context_ignore overlays on top.
var baselineContextIgnore = parseEmbeddedContextIgnoreBaseline()

// parseEmbeddedContextIgnoreBaseline reads the context_ignore_baseline list from the
// embedded charly.yml via the shared minimal decoder; panics if the embed is malformed or
// the directive is empty (a build-time invariant, never a runtime input).
func parseEmbeddedContextIgnoreBaseline() []string {
	var doc struct {
		ContextIgnoreBaseline []string `yaml:"context_ignore_baseline"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.ContextIgnoreBaseline) == 0 {
		panic("generate: embedded charly.yml has no context_ignore_baseline: directive")
	}
	return doc.ContextIgnoreBaseline
}

// contextIgnoreFiles are the two engine-native build-context ignore files charly
// generates. podman reads .containerignore (preferring it) or .dockerignore;
// docker reads only .dockerignore. Emitting both from one source covers both
// engines with no divergent hand-maintained dotfile.
var contextIgnoreFiles = []string{".containerignore", ".dockerignore"}

// writeContextIgnore renders the build-context exclude list
// (baselineContextIgnore + defaults.context_ignore) into BOTH
// .containerignore and .dockerignore at the project root (the build context
// root). Single source of values, two render targets — keeps podman and
// docker builds in lockstep without a hand-maintained dotfile. Insertion
// order is deterministic (fixed baseline, then author-ordered config),
// duplicates collapsed.
func (g *Generator) writeContextIgnore() error {
	seen := make(map[string]bool)
	var patterns []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		patterns = append(patterns, p)
	}
	for _, p := range baselineContextIgnore {
		add(p)
	}
	if g.Config != nil {
		for _, p := range g.Config.Defaults.ContextIgnore {
			add(p)
		}
	}

	var b strings.Builder
	for _, name := range contextIgnoreFiles {
		b.Reset()
		fmt.Fprintf(&b, "# %s (generated -- do not edit; source: defaults.context_ignore in charly.yml)\n", name)
		for _, p := range patterns {
			b.WriteString(p)
			b.WriteByte('\n')
		}
		if err := atomicWriteFile(filepath.Join(g.Dir, name), []byte(b.String()), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	return nil
}

// generateContainerfile generates a Containerfile for a single image
func (g *Generator) generateContainerfile(boxName string) error {
	// imageDir is NOT wiped here. A destructive RemoveAll+regenerate races
	// concurrent builds of a SHARED base image (two parallel beds both regenerate
	// .build/fedora/). The Containerfile is written ATOMICALLY (writeContainerfile)
	// and inline content is content-addressed (_inline/<layer>/<sha>), so any
	// stale leftover is unreferenced + harmless; cleanStaleBuildDirs removes whole
	// dirs for REMOVED images.
	imageDir := filepath.Join(g.BuildDir, boxName)

	img := g.Boxes[boxName]
	var b strings.Builder

	// Header
	fmt.Fprintf(&b, "# .build/%s/Containerfile (generated -- do not edit)\n\n", boxName)

	// Resolve candy order for this image
	var parentCandies map[string]bool
	if !img.IsExternalBase {
		var err error
		parentCandies, err = CandyProvidedByBox(img.Base, g.Boxes, g.Candies)
		if err != nil {
			return err
		}
	}

	candyOrder, err := g.globalOrderForBox(img.Candy, parentCandies)
	if err != nil {
		return err
	}

	// Data images: minimal FROM scratch with only data staging + labels
	if img.DataImage {
		return g.generateDataImageContainerfile(boxName, img, candyOrder, imageDir)
	}

	// ARG for base image must come first (before any FROM). For
	// `from: builder:<name>` images the resolved base is "scratch" and
	// the rootfs gets ADDed right after the FROM ${BASE_IMAGE} line below.
	resolvedBase := g.resolveBaseImage(img)
	fmt.Fprintf(&b, "ARG BASE_IMAGE=%s\n\n", resolvedBase)

	// Emit scratch stages for each candy
	g.emitScratchStages(&b, candyOrder)

	// Emit per-candy multi-stage build stages — fully config-driven from the embedded builder: vocabulary.
	if err := g.emitBuilderStages(&b, boxName, img, candyOrder); err != nil {
		return err
	}

	// Emit per-candy EXTERNAL builder stages — the build-time BUILDER leg: a candy
	// selecting an `external_builder:` (an out-of-tree builder plugin) gets the
	// provider's OpResolve stage spliced here, pre-main-FROM (the artifacts COPY
	// follows post-main-FROM via emitExternalBuilderArtifacts).
	if err := g.emitExternalBuilderStages(&b, img, candyOrder); err != nil {
		return err
	}

	// Emit extraction stages for candies with extract field
	g.emitExtractStages(&b, candyOrder)

	// Aggregate candy-contributed capabilities once for this image. Cache
	// onto ResolvedBox so downstream emit paths (and label emission)
	// don't recompute.
	caps, capsErr := AggregateCandyCapabilities(g.Candies, candyOrder)
	if capsErr != nil {
		return capsErr
	}
	img.CandyCaps = caps
	if missing := CheckRequiredCapabilities(g.Candies, candyOrder, caps); len(missing) > 0 {
		return CandyCapabilitiesError(g.Candies, candyOrder, missing)
	}

	// Detect active init systems from candies (driven by the embedded init: vocabulary config)
	activeInits := make(map[string]*InitDef)
	if img.InitConfig != nil {
		activeInits = img.InitConfig.ActiveInit(g.Candies, candyOrder)
	}
	// Store init system on ResolvedBox for downstream use (labels, etc.)
	if img.InitConfig != nil {
		img.InitSystem, img.InitDef = img.InitConfig.ResolveInitSystem(g.Candies, candyOrder, "")
	}

	// Detect route/traefik candies and emit the traefik-routes scratch stage.
	hasRoutes, hasTraefik, err := g.emitTraefikRouteStage(&b, boxName, img, candyOrder)
	if err != nil {
		return err
	}

	// Emit init system stages and learn which inits received fragment content.
	initHasFragments, err := g.emitInitFragmentStages(&b, boxName, img, candyOrder, activeInits)
	if err != nil {
		return err
	}

	// Main image
	b.WriteString("FROM ${BASE_IMAGE}\n\n")

	// `from: builder:<name>` — import the pre-built rootfs tarball that
	// was staged by runPrivilegedBuilders (see build.go preBuildHook).
	// The path is relative to the project-root build context, so it
	// dotted-out under .build/<image>/.
	if after, ok := strings.CutPrefix(img.From, "builder:"); ok {
		builderName := after
		fmt.Fprintf(&b, "ADD .build/%s/%s.tar.gz /\n\n", boxName, builderName)
	}

	// Bootstrap preamble (only for external base images, and only when
	// not coming from a builder rootfs — the bootstrap step expects an
	// upstream package manager which from: scratch + ADD doesn't have
	// pre-installed; that's handled by the builder's package set).
	if img.IsExternalBase && !strings.HasPrefix(img.From, "builder:") {
		g.writeBootstrap(&b, img)
	} else {
		// Internal base or builder rootfs - reset to root for candy processing
		b.WriteString("USER root\n\n")
	}

	// Collect and write environment variables from candies
	g.writeCandyEnv(&b, candyOrder, img)

	// Emit EXPOSE directives for the box's inherited candy ports
	g.writeExpose(&b, img.Name)

	// LABEL emission is deferred to the end of the final stage — see the
	// writeLabels call after the final USER directive below. Putting LABELs
	// last means a test/label edit only reruns the LABEL instructions
	// themselves (metadata-only, ~0ms on disk) instead of invalidating the
	// buildkit cache for every downstream RUN/COPY in a 100-step stack.

	// Copy builder artifacts — fully config-driven from the embedded builder: vocabulary copy_artifacts/copy_binary
	g.emitBuilderArtifacts(&b, img, candyOrder)

	// Copy EXTERNAL builder artifacts — the cached OpResolve reply's COPY --from
	// directives (the post-main-FROM half of the build-time BUILDER leg).
	g.emitExternalBuilderArtifacts(&b, candyOrder)

	// Bake out-of-tree `bake_plugin:` provider binaries into the FINAL image at
	// /usr/lib/charly/plugins/ — the BUILD-side half of the S0 baked-plugin seam, so a
	// deployed source/toolchain-less container can run an external plugin its
	// in-container charly needs (e.g. charly-mcp → plugin-mcp for `charly mcp serve`).
	if err := g.emitBakedPlugins(&b, boxName, candyOrder); err != nil {
		return err
	}

	// Copy extracted files from multi-stage builds
	g.emitExtractedFiles(&b, img, candyOrder)

	// Stage data files from data candies into /data/ for deploy-time provisioning
	g.writeDataStaging(&b, candyOrder, img)

	// Process each candy
	// Post-candy steps (init assembly, traefik, bootc) run as root,
	// so the last candy must reset to root only if such steps exist.
	needsRootAfter := len(activeInits) > 0 || (hasRoutes && hasTraefik) || (caps != nil && caps.NeedsRootAfterInit)
	inUserMode := false
	for i, candyName := range candyOrder {
		isLast := i == len(candyOrder)-1
		var werr error
		inUserMode, werr = g.writeCandySteps(&b, candyName, img, isLast && !needsRootAfter)
		if werr != nil {
			return werr
		}
	}

	// Assemble init system configs (driven by the embedded init: vocabulary templates)
	if err := g.emitInitAssembly(&b, img, candyOrder, activeInits, initHasFragments); err != nil {
		return err
	}

	// Copy traefik dynamic routes if needed
	if hasRoutes && hasTraefik {
		b.WriteString("# Traefik dynamic routes\n")
		b.WriteString("COPY --from=traefik-routes /routes.yml /etc/traefik/dynamic/routes.yml\n\n")
	}

	// Final USER directive (use UID for robustness)
	// Compositions that declare preserve_user (e.g. bootc-config) boot
	// with systemd managing user sessions — the container USER directive
	// is irrelevant. Other compositions reset to the unprivileged uid.
	if caps != nil && caps.PreserveUser {
		// leave as root — systemd handles user sessions
	} else if !inUserMode || needsRootAfter {
		fmt.Fprintf(&b, "USER %d\n", img.UID)
	}

	// Emit image metadata labels LAST so test/label edits don't invalidate
	// the buildkit cache for all upstream RUN/COPY steps. LABELs are pure
	// metadata (attach to the final image manifest) and have no functional
	// dependency on subsequent instructions — they're the ideal last-line
	// of the Containerfile.
	g.writeLabels(&b, boxName, candyOrder, img)

	// Ensure the image dir exists (it is no longer wiped at function start).
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		return err
	}

	content := b.String()
	g.Containerfiles[boxName] = content

	containerfile := filepath.Join(imageDir, "Containerfile")
	return writeContainerfile(containerfile, content)
}

// writeContainerfile validates the rendered Containerfile (catching Go-template
// render failures via #RenderedText — see /charly-internals:egress) and writes it.
func writeContainerfile(path, content string) error {
	if err := validateTextEgress(path, content); err != nil {
		return err
	}
	// Atomic write: a concurrent build reading this Containerfile (parallel
	// same-dir fan-out) always sees a complete file; concurrent writers of the
	// same deterministic content converge.
	return atomicWriteFile(path, []byte(content), 0644)
}

// emitScratchStages emits one `FROM scratch AS <candy>` + COPY pair per candy.
func (g *Generator) emitScratchStages(b *strings.Builder, candyOrder []string) {
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		stageName := layer.Name // use short name for stage alias
		fmt.Fprintf(b, "FROM scratch AS %s\n", stageName)
		fmt.Fprintf(b, "COPY %s/ /\n\n", g.candyCopySource(candyName))
	}
}

// emitBuilderStages emits per-candy multi-stage build stages. Each builder declares
// detect_files and/or detect_config (the embedded builder: vocabulary, host-side DETECTION);
// for each matching candy an EXTERNALIZED detection builder (pixi/npm/aur) renders its stage
// via its plugin's OpResolve leg (resolveDetectionBuilder → kit.BuilderResolve, C10 — no longer
// an in-core stage_template), while a custom non-externalized builder still renders its
// vocabulary stage_template. The externalized builder plugins are connected on-demand
// (ensureBuildersConnected). The reply is cached per (candy, builder) for emitBuilderArtifacts.
func (g *Generator) emitBuilderStages(b *strings.Builder, boxName string, img *ResolvedBox, candyOrder []string) error {
	if img.BuilderConfig == nil {
		return nil
	}
	// Reset the per-image detection-builder reply cache (populated below, read by
	// emitBuilderArtifacts). Scopes to ONE image, exactly like externalBuilderReplies.
	g.detectionBuilderReplies = map[string]spec.BuilderResolveReply{}

	// Connect the EXTERNALIZED detection-builder plugins this image triggers (on-demand,
	// scoped — the SAME machinery the deploy build PRE-PASS uses, R3), so their OpResolve
	// build leg can be Invoked below. A custom (non-externalized) vocab builder needs no
	// connect and renders from its own StageTemplate.
	if detected := detectExternalizedBuilders(candyOrder, g.Candies, img); len(detected) > 0 {
		if err := ensureBuildersConnected(context.Background(), g.Config, g.Dir, detected); err != nil {
			return fmt.Errorf("image %q: %w", boxName, err)
		}
	}

	// Process builders in deterministic order
	builderNames := img.BuilderConfig.BuilderNames()
	for _, builderName := range builderNames {
		builderDef := img.BuilderConfig.Builder[builderName]
		if builderDef.Inline {
			continue // inline builders handled in writeCandySteps
		}
		external := externalizedBuilders[builderName]
		// A non-externalized (custom) builder with no StageTemplate contributes no stage.
		// An externalized builder renders via its plugin's OpResolve regardless (its
		// StageTemplate moved out of the embedded vocabulary into kit.BuilderResolve, C10).
		if !external && builderDef.StageTemplate == "" {
			continue
		}
		for _, candyName := range candyOrder {
			layer := g.Candies[candyName]
			if !g.candyNeedsBuilder(img, layer, builderDef) {
				continue
			}
			builderRef := g.builderRefForFormat(boxName, builderName)
			if builderRef == "" {
				return fmt.Errorf("image %q: candy %q needs builder %q but no builders.%s configured", boxName, candyName, builderName, builderName)
			}
			if external {
				reply, err := g.resolveDetectionBuilder(builderName, layer, builderDef, img, builderRef)
				if err != nil {
					return fmt.Errorf("image %q: candy %q builder %q: %w", boxName, candyName, builderName, err)
				}
				if strings.TrimSpace(reply.Stage) == "" {
					return fmt.Errorf("image %q: candy %q: detection builder %q returned an empty OpResolve stage", boxName, candyName, builderName)
				}
				g.detectionBuilderReplies[builderCtxKey(layer.Name, builderName)] = reply
				b.WriteString(reply.Stage)
				b.WriteString("\n")
				continue
			}
			ctx := g.buildStageContext(layer, builderName, builderDef, img, builderRef)
			rendered, err := RenderTemplate(builderName+"-stage", builderDef.StageTemplate, ctx)
			if err != nil {
				return fmt.Errorf("image %q: rendering %s stage for candy %q: %w", boxName, builderName, candyName, err)
			}
			b.WriteString(rendered)
			b.WriteString("\n")
		}
	}
	return nil
}

// resolveDetectionBuilder Invokes an externalized detection-builder plugin's OpResolve build
// leg for one (candy, builder), returning the rendered BuilderResolveReply (Stage +
// CopyArtifacts + CopyBinary). The host computes the render context host-side (buildStageContext
// + pre-rendered cache mounts) and the plugin (kit.BuilderResolve) renders the multi-stage — the
// C10 counterpart of resolveExternalBuilder for the `external_builder:` path, sharing the SAME
// OpResolve Invoke helper (resolveBuilderStage, R3).
func (g *Generator) resolveDetectionBuilder(builderName string, layer *Candy, builderDef *BuilderDef, img *ResolvedBox, builderRef string) (spec.BuilderResolveReply, error) {
	var zero spec.BuilderResolveReply
	prov, ok := providerRegistry.ResolveBuilder(builderName)
	if !ok {
		return zero, fmt.Errorf("detection builder %q is externalized but its plugin is not connected (a plugin-load gap?)", builderName)
	}
	ctx := g.buildStageContext(layer, builderName, builderDef, img, builderRef)
	in := builderResolveInputFrom(layer.Name, builderName, builderDef, ctx)
	return resolveBuilderStage(prov, builderName, in, img)
}

// builderResolveInputFrom builds the serializable spec.BuilderResolveInput a builder plugin's
// OpResolve leg needs, from the host-computed BuildStageContext. Cache mounts are PRE-RENDERED
// to flag strings here (the host owns the cache_mount vocab + RenderCacheMounts) with the SAME
// separator/trailing the former cacheMountsOwned/cacheMountsAuto template funcs used, so the
// plugin's rendered stage is byte-identical to the former embedded-vocabulary render. Shared by
// the box-build path (resolveDetectionBuilder) AND the pod-overlay build-emit (stepEmitBuilder), R3.
func builderResolveInputFrom(candyName, builderName string, builderDef *BuilderDef, ctx *BuildStageContext) spec.BuilderResolveInput {
	return spec.BuilderResolveInput{
		Candy:            candyName,
		Builder:          builderName,
		BuilderRef:       ctx.BuilderRef,
		StageName:        ctx.StageName,
		LayerStage:       ctx.LayerStage,
		CopySrc:          ctx.CopySrc,
		UID:              ctx.UID,
		GID:              ctx.GID,
		Home:             ctx.Home,
		User:             ctx.User,
		Manifest:         ctx.Manifest,
		HasLockFile:      ctx.HasLockFile,
		InstallCmd:       ctx.InstallCmd,
		ManylinuxFix:     ctx.ManylinuxFix,
		HasBuildScript:   ctx.HasBuildScript,
		BuildScript:      ctx.BuildScript,
		Packages:         ctx.Packages,
		Options:          ctx.Options,
		CacheMountsOwned: RenderCacheMounts(ctx.CacheMounts, ctx.UID, ctx.GID, " \\\n    ", true),
		CacheMountsAuto:  RenderCacheMountsAuto(ctx.CacheMounts, ctx.UID, ctx.GID, " \\\n    ", false),
		Inline:           builderDef.Inline,
	}
}

// emitExternalBuilderStages emits the pre-main-FROM multi-stage block for every
// candy that selects an `external_builder:` — the build-time BUILDER leg, the
// multi-stage counterpart of a `run:` step's `plugin:` verb (emitPluginFragment).
// For each such candy it resolves the builder word through providerRegistry; an
// EXTERNAL provider (a *grpcProvider connected by the build-time plugin connect seam
// in NewGenerator) is Invoked with OpResolve and the returned BuilderResolveReply's
// Stage is written verbatim (egress-validated with the rest of the Containerfile).
// The reply is CACHED on the Generator so emitExternalBuilderArtifacts can splice the
// matching COPY --from directives post-main-FROM without re-Invoking. An unresolvable
// external_builder, a non-external (compiled-in) provider, or an Invoke error (including a
// detection-builder plugin rejecting OpResolve — see below) fails LOUDLY (R4) — never a
// silently-dropped builder stage. The cache is RESET here so it scopes to ONE image.
func (g *Generator) emitExternalBuilderStages(b *strings.Builder, img *ResolvedBox, candyOrder []string) error {
	g.externalBuilderReplies = map[string]spec.BuilderResolveReply{}
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		word := layer.ExternalBuilder
		if word == "" {
			continue
		}
		prov, ok := providerRegistry.ResolveBuilder(word)
		if !ok {
			return fmt.Errorf("candy %q: external_builder %q is not a registered builder (an external plugin not connected at build time?)", candyName, word)
		}
		// Only an EXTERNAL out-of-process builder (a *grpcProvider) drives this build-time
		// OpResolve path; reject any compiled-in provider (defensive — no in-proc builder exists
		// today). NOTE: the four detection-builders (pixi/cargo/npm/aur) are ALSO external
		// grpcProviders now, but they serve only the DEPLOY-time OpCollectContext/OpReverse legs and
		// are SELECTED BY DETECTION (their detect-files / aur: section via the embedded builder:
		// vocabulary), never by external_builder:. Mis-selecting one here passes this type-assert but
		// then fails LOUDLY at resolveExternalBuilder's OpResolve Invoke (the plugin rejects the op).
		if _, isExternal := prov.(*grpcProvider); !isExternal {
			return fmt.Errorf("candy %q: external_builder %q resolves to a compiled-in builder, not an external plugin", candyName, word)
		}
		reply, err := resolveExternalBuilder(prov, word, candyName, img)
		if err != nil {
			return fmt.Errorf("candy %q: external_builder %q resolve: %w", candyName, word, err)
		}
		g.externalBuilderReplies[candyName] = reply
		b.WriteString(reply.Stage)
		if !strings.HasSuffix(reply.Stage, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return nil
}

// emitExternalBuilderArtifacts writes the post-main-FROM COPY --from directives for
// each candy whose external builder resolved in emitExternalBuilderStages (read from
// the per-image cache — never re-Invoked). The artifacts pull the built files out of
// the plugin's multi-stage build into the final image.
func (g *Generator) emitExternalBuilderArtifacts(b *strings.Builder, candyOrder []string) {
	for _, candyName := range candyOrder {
		reply, ok := g.externalBuilderReplies[candyName]
		if !ok || len(reply.CopyArtifacts) == 0 {
			continue
		}
		fmt.Fprintf(b, "# Copy external_builder artifacts (%s)\n", g.Candies[candyName].ExternalBuilder)
		for _, line := range reply.CopyArtifacts {
			b.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}
}

// resolveBuilderStage is the SHARED OpResolve Invoke+decode for the builder BUILDER leg (R3 —
// ONE OpResolve path serving BOTH the `external_builder:`-selected out-of-tree builders and the
// four DETECTION-builders). It marshals the render context (spec.BuilderResolveInput) as op.Params
// and a spec.BuildEnv descriptor as op.Env (so a plugin can tailor per distro/image), Invokes the
// provider's OpResolve, and returns the decoded reply UNVALIDATED — the caller enforces the
// emptiness rule appropriate to its path (external_builder + detection multi-stage require a
// non-empty Stage; the inline cargo path requires a non-empty InlineFragment).
func resolveBuilderStage(prov Provider, word string, in spec.BuilderResolveInput, img *ResolvedBox) (spec.BuilderResolveReply, error) {
	var zero spec.BuilderResolveReply
	params, err := marshalJSON(in)
	if err != nil {
		return zero, fmt.Errorf("marshal builder resolve input: %w", err)
	}
	env, err := marshalJSON(spec.BuildEnv{Distros: img.Tags, Image: img.Name})
	if err != nil {
		return zero, fmt.Errorf("marshal build env: %w", err)
	}
	res, err := prov.Invoke(context.Background(), &Operation{Reserved: word, Op: OpResolve, Params: params, Env: env})
	if err != nil {
		return zero, err
	}
	var reply spec.BuilderResolveReply
	if err := json.Unmarshal(res.JSON, &reply); err != nil {
		return zero, fmt.Errorf("decode OpResolve reply: %w", err)
	}
	return reply, nil
}

// resolveExternalBuilder Invokes an `external_builder:`-selected out-of-tree builder provider's
// OpResolve and returns the decoded BuilderResolveReply — the BUILDER-leg analogue of
// emitPluginFragment. It sends a MINIMAL render context (the requesting candy name only — an
// out-of-tree builder renders a self-contained stage that reads none of the detection fields),
// then requires a non-empty Stage (a mis-selected word producing no build-context builder fails
// LOUDLY). Shares the OpResolve Invoke with the detection path via resolveBuilderStage (R3).
func resolveExternalBuilder(prov Provider, word, candyName string, img *ResolvedBox) (spec.BuilderResolveReply, error) {
	var zero spec.BuilderResolveReply
	reply, err := resolveBuilderStage(prov, word, spec.BuilderResolveInput{Candy: candyName}, img)
	if err != nil {
		return zero, err
	}
	if strings.TrimSpace(reply.Stage) == "" {
		return zero, fmt.Errorf("external builder %q returned an empty OpResolve stage — it has no build-context builder", word)
	}
	return reply, nil
}

// emitBakedPlugins bakes each composing candy's `bake_plugin:` out-of-tree plugin
// binaries into the FINAL image at bakedPluginDir (/usr/lib/charly/plugins/), so a
// DEPLOYED container — which has neither the candy source nor a go toolchain — can run
// an external plugin its in-container charly needs at runtime. It is the BUILD-side half
// of the S0 baked-plugin seam, the deploy-time counterpart of resolvePluginBinary's
// bakedPluginBinary fallback (plugin_loader.go): the loader looks for the binary at
// $CHARLY_PLUGIN_DIR/<bakedPluginFileName(name)> then bakedPluginDir/<bakedPluginFileName(name)>,
// so the COPY destination here uses the SAME bakedPluginFileName helper (plugin_loader.go,
// R3). It keys by the plugin candy's LEAF name, NOT the full scanned-set key: the BUILD may
// resolve the candy under an @github ref while the in-container project sees it bare, so the
// only identity both halves agree on is the leaf.
//
// Called post-main-FROM (right after emitExternalBuilderArtifacts) so the COPY lands in
// the final stage. For each referenced plugin it resolves the candy's SOURCE DIR the SAME
// way loadProjectPlugins does — g.Candies[key].SourceDir on the scanned set
// (ScanAllCandyWithConfig) — host-builds the provider binary (buildPluginBinary; the SAME
// host build the loader runs), stages it into the per-image build context under
// .build/<boxName>/.plugins/, and emits the COPY + chmod. The binary is CGO-free Go, so it
// is portable to a SAME-ARCH container; cross-arch baking is a future concern. Dedup is by
// plugin map-key so a plugin baked by two composing candies is built + copied once.
func (g *Generator) emitBakedPlugins(b *strings.Builder, boxName string, candyOrder []string) error {
	baked := map[string]struct{}{}
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer == nil || len(layer.BakePlugin) == 0 {
			continue
		}
		for _, ref := range layer.BakePlugin {
			// key is the g.Candies map key (used for SourceDir resolution); the baked
			// FILENAME derives from its leaf via bakedPluginFileName — the stable identity
			// the build-side and the in-container loader agree on across local/@github refs.
			key := ref.Bare()
			if _, done := baked[key]; done {
				continue
			}
			baked[key] = struct{}{}
			plugin := g.Candies[key]
			if plugin == nil {
				return fmt.Errorf("candy %q: bake_plugin %q is not a known plugin candy (not in the scanned candy set)", candyName, key)
			}
			if plugin.SourceDir == "" {
				return fmt.Errorf("candy %q: bake_plugin %q has no source dir to build from", candyName, key)
			}
			binPath, err := buildPluginBinary(context.Background(), plugin.SourceDir, key)
			if err != nil {
				return fmt.Errorf("candy %q: bake_plugin %q: %w", candyName, key, err)
			}
			binName := bakedPluginFileName(key)
			stageDir := filepath.Join(g.BuildDir, boxName, ".plugins")
			if err := os.MkdirAll(stageDir, 0o755); err != nil {
				return fmt.Errorf("candy %q: bake_plugin %q: stage dir: %w", candyName, key, err)
			}
			if err := copyFileBytes(binPath, filepath.Join(stageDir, binName)); err != nil {
				return fmt.Errorf("candy %q: bake_plugin %q: stage binary: %w", candyName, key, err)
			}
			ctxRel := fmt.Sprintf(".build/%s/.plugins/%s", boxName, binName)
			dest := bakedPluginDir + "/" + binName
			fmt.Fprintf(b, "# Bake plugin %q (required by %q) for in-container charly\n", key, candyName)
			fmt.Fprintf(b, "COPY %s %s\n", ctxRel, dest)
			fmt.Fprintf(b, "RUN chmod 0755 %s\n", dest)
			// Bake a `.providers` words manifest beside the binary so the in-container prescan
			// (discoverBakedPluginWords) registers the plugin's command word into the grammar
			// WITHOUT building/connecting it — the binary is resolved + fork/exec'd lazily on
			// dispatch (dispatchExternalCommand's baked path), so an unrelated `charly <cmd>` in
			// the container pays nothing.
			if plugin.Plugin != nil && len(plugin.Plugin.Providers) > 0 {
				lines := make([]string, len(plugin.Plugin.Providers))
				for i, c := range plugin.Plugin.Providers {
					lines[i] = string(c) // PluginCapability is a "<class>:<word>" string
				}
				manifest := strings.Join(lines, "\n") + "\n"
				if err := os.WriteFile(filepath.Join(stageDir, binName+".providers"), []byte(manifest), 0o644); err != nil {
					return fmt.Errorf("candy %q: bake_plugin %q: stage manifest: %w", candyName, key, err)
				}
				fmt.Fprintf(b, "COPY %s.providers %s.providers\n", ctxRel, dest)
			}
			b.WriteString("\n")
		}
	}
	return nil
}

// emitExtractStages emits a `FROM <source> AS <candy>-extract-<i>` stage for
// every extract entry across the candy chain.
func (g *Generator) emitExtractStages(b *strings.Builder, candyOrder []string) {
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if !layer.HasExtract() {
			continue
		}
		for i, ext := range layer.Extract() {
			stageName := fmt.Sprintf("%s-extract-%d", candyName, i)
			fmt.Fprintf(b, "FROM %s AS %s\n\n", ext.Source, stageName)
		}
	}
}

// emitBuilderArtifacts copies builder artifacts/binaries into the main image. For an
// EXTERNALIZED detection builder (pixi/npm/aur) the COPY lines come from the plugin's cached
// OpResolve reply (detectionBuilderReplies, populated by emitBuilderStages) — the plugin owns
// the copy_artifact/copy_binary shape now (C10). For a custom (non-externalized) builder they
// come from the embedded builder: vocabulary copy_artifacts/copy_binary. Both preserve the
// former structure: the `# Copy <builder> artifacts` header once per builder, then each candy's
// artifacts, and the builder BINARY copied ONCE per builder (deduped across candies).
func (g *Generator) emitBuilderArtifacts(b *strings.Builder, img *ResolvedBox, candyOrder []string) {
	if img.BuilderConfig == nil {
		return
	}
	builderNames := img.BuilderConfig.BuilderNames()
	for _, builderName := range builderNames {
		builderDef := img.BuilderConfig.Builder[builderName]
		if builderDef.Inline {
			continue
		}
		external := externalizedBuilders[builderName]
		if !external && builderDef.StageTemplate == "" {
			continue
		}

		// Find candies that triggered this builder
		hasArtifacts := false
		binaryCopied := false
		for _, candyName := range candyOrder {
			layer := g.Candies[candyName]
			if !g.candyNeedsBuilder(img, layer, builderDef) {
				continue
			}

			if external {
				reply, ok := g.detectionBuilderReplies[builderCtxKey(layer.Name, builderName)]
				if !ok {
					continue
				}
				for _, line := range reply.CopyArtifacts {
					if !hasArtifacts {
						fmt.Fprintf(b, "# Copy %s artifacts\n", builderName)
						hasArtifacts = true
					}
					b.WriteString(line)
					b.WriteString("\n")
				}
				if reply.CopyBinary != "" && !binaryCopied {
					b.WriteString(reply.CopyBinary)
					b.WriteString("\n")
					binaryCopied = true
				}
				continue
			}

			stageName := fmt.Sprintf("%s-%s-build", layer.Name, builderName)

			// Copy artifacts
			for _, art := range builderDef.CopyArtifacts {
				if !hasArtifacts {
					fmt.Fprintf(b, "# Copy %s artifacts\n", builderName)
					hasArtifacts = true
				}
				src := expandBuilderPath(art.Src, img)
				dst := expandBuilderPath(art.Dst, img)
				if art.Chown {
					fmt.Fprintf(b, "COPY --from=%s --chown=%d:%d %s %s\n", stageName, img.UID, img.GID, src, dst)
				} else {
					fmt.Fprintf(b, "COPY --from=%s %s %s\n", stageName, src, dst)
				}
			}

			// Copy binary (only once, from first matching candy)
			if builderDef.CopyBinary != nil && !binaryCopied {
				fmt.Fprintf(b, "COPY --from=%s %s %s\n", stageName, builderDef.CopyBinary.Src, builderDef.CopyBinary.Dst)
				binaryCopied = true
			}
		}
		if hasArtifacts || binaryCopied {
			b.WriteString("\n")
		}
	}
}

// emitExtractedFiles copies extracted files from multi-stage build stages into
// the main image.
func (g *Generator) emitExtractedFiles(b *strings.Builder, img *ResolvedBox, candyOrder []string) {
	hasExtract := false
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if !layer.HasExtract() {
			continue
		}
		if !hasExtract {
			b.WriteString("# Copy extracted files from Docker images\n")
			b.WriteString("USER root\n")
			hasExtract = true
		}
		for i, ext := range layer.Extract() {
			stageName := fmt.Sprintf("%s-extract-%d", candyName, i)
			fmt.Fprintf(b, "COPY --from=%s --chown=%d:%d %s %s\n",
				stageName, img.UID, img.GID, ext.Path, ext.Dest)
		}
	}
	if hasExtract {
		b.WriteString("\n")
	}
}

// emitInitAssembly assembles init system configs (driven by the embedded init:
// vocabulary templates): the assembly template, system-level service enablement,
// and any post-assembly step, per active init system.
func (g *Generator) emitInitAssembly(b *strings.Builder, img *ResolvedBox, candyOrder []string, activeInits map[string]*InitDef, initHasFragments map[string]bool) error {
	for initName, def := range activeInits {
		// assembly_template bind-mounts from the scratch stage emitted above;
		// skip it when no fragments were contributed (stage was not emitted).
		// system_enable_template and post_assembly_template are independent
		// and still run below.
		if initHasFragments[initName] {
			assembly, err := initRenderAssemblyTemplate(def)
			if err != nil {
				return fmt.Errorf("rendering assembly for %s: %w", initName, err)
			}
			if assembly != "" {
				b.WriteString(assembly)
				if !strings.HasSuffix(assembly, "\n") {
					b.WriteString("\n")
				}
				b.WriteString("\n")
			}
		}

		// System-level service enablement (e.g., systemctl enable sshd).
		// Collect every use_packaged: entry across the candy chain — these
		// are the distro-shipped systemd units the init system must enable.
		var systemUnits []string
		for _, candyName := range candyOrder {
			layer := g.Candies[candyName]
			for i := range layer.Service() {
				entry := &layer.Service()[i]
				if entry.IsPackaged() && entry.EffectiveScope() == "system" &&
					serviceEntryAppliesToDistro(entry, img.Distro) {
					systemUnits = append(systemUnits, entry.UsePackaged)
				}
			}
		}
		sysEnable, err := initRenderSystemEnableTemplate(def, systemUnits)
		if err != nil {
			return fmt.Errorf("rendering system enable for %s: %w", initName, err)
		}
		if sysEnable != "" {
			b.WriteString(sysEnable)
			if !strings.HasSuffix(sysEnable, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}

		// Post-assembly step (e.g., bootc container lint)
		postAssembly, err := initRenderPostAssemblyTemplate(def)
		if err != nil {
			return fmt.Errorf("rendering post-assembly for %s: %w", initName, err)
		}
		if postAssembly != "" {
			b.WriteString(postAssembly)
			if !strings.HasSuffix(postAssembly, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}
	return nil
}

// emitTraefikRouteStage detects whether the image has route candies plus the
// traefik candy and, when both are present, generates the traefik routes and
// emits the traefik-routes scratch stage. Returns the two detection flags
// (consumed downstream for the root-reset decision and the routes COPY).
func (g *Generator) emitTraefikRouteStage(b *strings.Builder, boxName string, img *ResolvedBox, candyOrder []string) (hasRoutes, hasTraefik bool, err error) {
	// Check if this image has route candies and traefik
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.HasRoute() {
			hasRoutes = true
		}
		if layer.Name == "traefik" {
			hasTraefik = true
		}
	}

	// Generate traefik routes only when traefik is actually present
	if hasRoutes && hasTraefik {
		if rerr := g.generateTraefikRoutes(boxName, candyOrder, img); rerr != nil {
			return hasRoutes, hasTraefik, rerr
		}
		b.WriteString("FROM scratch AS traefik-routes\n")
		fmt.Fprintf(b, "COPY .build/%s/traefik-routes.yml /routes.yml\n\n", boxName)
	}
	return hasRoutes, hasTraefik, nil
}

// emitInitFragmentStages emits the per-init scratch stages that COPY service
// fragments, relay configs, and detected service files, and returns the
// per-init map of whether any fragment content was contributed.
//
// When a child image adds services, parent-provided configs are included so the
// assembled config contains all services from the full chain. The returned map
// is consumed by emitInitAssembly: a candy chain contributing only via
// `system_services:` (plain unit names) COPYs no fragment files, so emitting an
// empty `FROM scratch AS <stage>` plus the `assembly_template` RUN that
// bind-mounts from it would fail at build time with "no such file or directory".
func (g *Generator) emitInitFragmentStages(b *strings.Builder, boxName string, img *ResolvedBox, candyOrder []string, activeInits map[string]*InitDef) (map[string]bool, error) {
	initHasFragments := map[string]bool{}
	for initName, def := range activeInits {
		initCandyOrder := candyOrder
		if !img.IsExternalBase {
			full := collectAllBoxCandies(boxName, g.Boxes, g.Candies)
			if len(full) > 0 {
				initCandyOrder = full
			}
		}
		if err := g.generateInitFragments(boxName, initName, def, initCandyOrder); err != nil {
			return nil, err
		}

		// Pre-scan the candy chain to decide whether this init has any fragment
		// content. If not, skip both the scratch stage emission and the
		// assembly_template RUN (see emitInitAssembly).
		hasFragments := false
		for _, candyName := range initCandyOrder {
			layer := g.Candies[candyName]
			if def.Model == "fragment_assembly" && layer.HasInit(initName) {
				hasFragments = true
				break
			}
			if initHasRelayTemplate(def) && len(layer.PortRelayPorts) > 0 {
				hasFragments = true
				break
			}
			if def.Model == "file_copy" && len(layer.ServiceFiles()) > 0 {
				hasFragments = true
				break
			}
		}
		initHasFragments[initName] = hasFragments
		if !hasFragments {
			continue
		}

		// Emit scratch stage with COPY lines for fragments
		fmt.Fprintf(b, "FROM scratch AS %s\n", def.StageName)
		if def.StageHeaderCopy != "" {
			headerCopy, err := g.rewriteHeaderCopyForRemote(def.StageHeaderCopy)
			if err != nil {
				return nil, err
			}
			b.WriteString(headerCopy + "\n")
		}
		for i, candyName := range initCandyOrder {
			layer := g.Candies[candyName]
			// Service content fragments (fragment_assembly model)
			if def.Model == "fragment_assembly" && layer.HasInit(initName) {
				// Use the SHORT name (not the map key) — a remote candy's key is
				// a slashed github ref that would create bogus nested dirs.
				fileName := fmt.Sprintf("%02d-%s.conf", i+1, layer.Name)
				copyLine, err := initRenderStageFragmentCopy(def, boxName, fileName)
				if err != nil {
					return nil, fmt.Errorf("rendering stage fragment copy for %s/%s: %w", initName, candyName, err)
				}
				b.WriteString(copyLine + "\n")
			}
			// Relay fragments
			if initHasRelayTemplate(def) && len(layer.PortRelayPorts) > 0 {
				for _, port := range layer.PortRelayPorts {
					confName := fmt.Sprintf("%02d-relay-%d.conf", i+1, port)
					copyLine, err := initRenderStageFragmentCopy(def, boxName, confName)
					if err != nil {
						return nil, fmt.Errorf("rendering relay copy for %s/%s port %d: %w", initName, candyName, port, err)
					}
					b.WriteString(copyLine + "\n")
				}
			}
			// File copy model: copy detected service files
			if def.Model == "file_copy" && len(layer.ServiceFiles()) > 0 {
				for _, svcPath := range layer.ServiceFiles() {
					svcName := filepath.Base(svcPath)
					copyLine, err := initRenderStageFragmentCopy(def, boxName, svcName)
					if err != nil {
						return nil, fmt.Errorf("rendering service file copy for %s/%s: %w", initName, candyName, err)
					}
					b.WriteString(copyLine + "\n")
				}
			}
		}
		b.WriteString("\n")
	}
	return initHasFragments, nil
}

// generateDataImageContainerfile produces a minimal FROM scratch Containerfile
// with only data staging COPY instructions and OCI labels. No runtime, no init,
// no packages, no builder stages.
func (g *Generator) generateDataImageContainerfile(boxName string, img *ResolvedBox, candyOrder []string, imageDir string) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# .build/%s/Containerfile (generated -- do not edit)\n\n", boxName)
	b.WriteString("FROM scratch\n\n")

	// Scratch stages for candies that have data
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if !layer.HasData() {
			continue
		}
		fmt.Fprintf(&b, "FROM scratch AS %s\n", layer.Name)
		fmt.Fprintf(&b, "COPY %s/ /\n\n", g.candyCopySource(candyName))
	}

	// Main image: just data staging + labels
	b.WriteString("FROM scratch\n\n")

	// Data staging COPY instructions
	g.writeDataStaging(&b, candyOrder, img)

	// Minimal labels (no init, no services, no ports). Content-derived
	// EffectiveVersion (not the per-build tag) — see writeLabels.
	b.WriteString("# Image metadata\n")
	fmt.Fprintf(&b, "LABEL %s=%q\n", LabelVersion, img.EffectiveVersion)
	fmt.Fprintf(&b, "LABEL %s=%q\n", LabelBox, boxName)
	if img.Registry != "" {
		fmt.Fprintf(&b, "LABEL %s=%q\n", LabelRegistry, img.Registry)
	}
	fmt.Fprintf(&b, "LABEL %s=%q\n", LabelDataBox, "true")

	// Data entries label
	var dataEntries []LabelDataEntry
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if !layer.HasData() {
			continue
		}
		for _, d := range layer.Data() {
			staging := "/data/" + d.Volume + "/"
			if d.Dest != "" {
				staging += d.Dest
				if !strings.HasSuffix(staging, "/") {
					staging += "/"
				}
			}
			dataEntries = append(dataEntries, LabelDataEntry{
				Volume:  d.Volume,
				Staging: staging,
				Candy:   candyName,
				Dest:    d.Dest,
			})
		}
	}
	if len(dataEntries) > 0 {
		writeJSONLabel(&b, LabelDataEntries, dataEntries)
	}

	// Volume labels (so charly config knows what volumes data targets)
	volumes, _ := CollectBoxVolume(g.Config, g.Candies, boxName, img.Home, nil)
	if len(volumes) > 0 {
		labelVols := make([]LabelVolumeEntry, 0, len(volumes))
		for _, v := range volumes {
			shortName := strings.TrimPrefix(v.VolumeName, "charly-"+boxName+"-")
			labelVols = append(labelVols, LabelVolumeEntry{Name: shortName, Path: v.ContainerPath})
		}
		writeJSONLabel(&b, LabelVolume, labelVols)
	}

	// Candy versions
	candyVersions := make(map[string]string)
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.Version != "" {
			candyVersions[candyName] = layer.Version
		}
	}
	writeJSONLabel(&b, LabelCandyVersion, candyVersions)

	b.WriteString("\n")

	// Write to disk
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		return err
	}
	content := b.String()
	g.Containerfiles[boxName] = content
	return writeContainerfile(filepath.Join(imageDir, "Containerfile"), content)
}

// resolveBaseImage returns the full base image reference.
// For internal bases, uses the exact CalVer tag so each image references
// the precise version of its parent. Both Docker and Podman resolve local
// images before pulling from registry.
func (g *Generator) resolveBaseImage(img *ResolvedBox) string {
	if img.IsExternalBase {
		return img.Base
	}
	parentImg := g.Boxes[img.Base]
	return parentImg.FullTag
}

// builderRefForFormat returns the full tag of the builder image for a given format,
// or "" if no builder is configured for that format.
func (g *Generator) builderRefForFormat(boxName, format string) string {
	img := g.Boxes[boxName]
	builder := img.Builder.BuilderFor(format)
	if builder == "" || builder == boxName {
		return ""
	}
	if builderImg, ok := g.Boxes[builder]; ok {
		return builderImg.FullTag
	}
	return ""
}

// renderDnfConfWrite returns a bootstrap-RUN fragment that appends the
// configured dnf download-speed knobs to /etc/dnf/dnf.conf, terminated with
// ` && \` so it chains into the rest of the bootstrap RUN. Returns "" when the
// distro has no dnf config or no knobs set (so non-dnf distros and unset
// configs emit nothing). The keys land under the file's [main] section
// (Fedora's stock dnf.conf is [main]-only).
func renderDnfConfWrite(d *DnfConfig) string {
	if d == nil {
		return ""
	}
	var lines []string
	if d.MaxParallelDownloads > 0 {
		lines = append(lines, fmt.Sprintf("max_parallel_downloads=%d", d.MaxParallelDownloads))
	}
	if d.Fastestmirror {
		lines = append(lines, "fastestmirror=True")
	}
	if len(lines) == 0 {
		return ""
	}
	body := strings.Join(lines, "\\n") + "\\n"
	return fmt.Sprintf("printf '%s' >> /etc/dnf/dnf.conf && \\\n    ", body)
}

// writeBootstrap writes the bootstrap preamble for external base images.
// All distro-specific behavior is driven by the embedded distro: vocabulary config.
func (g *Generator) writeBootstrap(b *strings.Builder, img *ResolvedBox) {
	b.WriteString("# Bootstrap\n")

	// Resolve distro config for bootstrap commands
	var distroDef *DistroDef
	if img.DistroConfig != nil {
		distroDef = img.DistroConfig.ResolveDistro(img.Distro)
	}

	b.WriteString("RUN ")
	// Cache mounts from distro config (or fall back to primary format's mounts)
	var cacheMounts []CacheMountDef
	if distroDef != nil {
		cacheMounts = distroDef.Bootstrap.CacheMount
	} else if img.DistroDef != nil {
		if formatDef, ok := img.DistroDef.Format[img.Pkg]; ok {
			cacheMounts = formatDef.CacheMount
		}
	}
	b.WriteString(RenderCacheMounts(cacheMounts, -1, 0, " \\\n    ", true))

	// dnf download tuning (max_parallel_downloads / fastestmirror) → written to
	// /etc/dnf/dnf.conf BEFORE the bootstrap install, so it speeds up the
	// bootstrap install itself AND every per-candy dnf install in this image
	// and its descendants. Speed-only — never changes package selection.
	if distroDef != nil {
		b.WriteString(renderDnfConfWrite(distroDef.Dnf))
	}

	// Install bootstrap packages using distro's install command
	if distroDef != nil && distroDef.Bootstrap.InstallCmd != "" && len(distroDef.Bootstrap.Package) > 0 {
		fmt.Fprintf(b, "%s %s && \\\n    ", distroDef.Bootstrap.InstallCmd, strings.Join(distroDef.Bootstrap.Package, " "))
	}

	// Apply distro-specific workarounds
	if distroDef != nil {
		for _, w := range distroDef.Workarounds {
			b.WriteString(w + " && \\\n    ")
		}
	}

	b.WriteString("{ [ -L /usr/local ] && mkdir -p \"$(readlink /usr/local)\"; mkdir -p /usr/local/bin; } && \\\n")
	b.WriteString("    ARCH=$(uname -m) && \\\n")
	b.WriteString("    case \"$ARCH\" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac && \\\n")
	b.WriteString("    curl -fsSL \"https://github.com/go-task/task/releases/latest/download/task_linux_${ARCH}.tar.gz\" | tar -xzf - -C /usr/local/bin task\n\n")

	// User/group handling — two modes driven by resolved user_policy:
	//   UserAdopted=true  → base image already ships this user at the
	//                        declared uid/gid/home; skip creation.
	//   UserAdopted=false → idempotent create (no-op if uid already taken;
	//                        the policy layer blocks the collision case
	//                        via auto/create semantics before we get here).
	if img.UserAdopted {
		fmt.Fprintf(b, "# User %s (uid=%d) adopted from base image (declared in the embedded distro.base_user) — no useradd needed\n\n", img.User, img.UID)
	} else {
		fmt.Fprintf(b, "RUN if ! getent passwd %d >/dev/null 2>&1; then \\\n", img.UID)
		fmt.Fprintf(b, "      (getent group %d >/dev/null 2>&1 || groupadd -g %d %s) && \\\n", img.GID, img.GID, img.User)
		fmt.Fprintf(b, "      useradd -m -u %d -g %d -s /bin/bash %s; \\\n", img.UID, img.GID, img.User)
		b.WriteString("    fi\n\n")
	}

	// WORKDIR only - ENV comes from candy env files
	fmt.Fprintf(b, "WORKDIR %s\n\n", img.Home)
}

// escapeContainerfileEnvValue prefixes `\` to every `$` so Docker's ENV-
// value substitution treats the rest as literal text. The escape is
// preserved through the shell invocations that consume the env value at
// runtime (bash strips a single `\$` to `$` and substitutes the var as
// expected). Without this escape, references like `${POSTGRES_PASSWORD}`
// in env: block values get emptied by Docker at build time because
// POSTGRES_PASSWORD is not a build arg — it's a runtime-injected secret.
//
// Special exception: leave `${PATH}` intact. The path-append code at the
// PATH ENV directive depends on Docker substituting the parent layer's
// PATH value during the build. PATH is the only ENV var charly knows is set
// at every Containerfile build step (Dockerfile spec guarantees it).
func escapeContainerfileEnvValue(v string) string {
	// Replace $ with \$ EXCEPT in `${PATH}` references (rare but documented).
	// Two-step: protect ${PATH}, escape, restore.
	const sentinel = "\x00CHARLY_PATH_REF\x00"
	v = strings.ReplaceAll(v, "${PATH}", sentinel)
	v = strings.ReplaceAll(v, "$", "\\$")
	v = strings.ReplaceAll(v, sentinel, "${PATH}")
	return v
}

// writeCandyEnv collects env configs from all candies and writes ENV directives.
// Builder-triggered runtime env contributions (RuntimeEnv + PathContributions
// on BuilderDef) are merged in alongside candy contributions — see
// collectBuilderRuntimeEnv.
func (g *Generator) writeCandyEnv(b *strings.Builder, candyOrder []string, img *ResolvedBox) {
	var configs []*EnvConfig

	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.HasEnv() {
			cfg, err := layer.EnvConfig()
			if err == nil && cfg != nil {
				configs = append(configs, cfg)
			}
		}
	}

	configs = append(configs, g.collectBuilderRuntimeEnv(candyOrder, img)...)

	if len(configs) == 0 {
		return
	}

	// Merge all configs
	merged := MergeEnvConfigs(configs)

	// Expand paths with home directory
	expanded := ExpandEnvConfig(merged, img.Home)

	// Write ENV directives
	if len(expanded.Vars) > 0 || len(expanded.PathAppend) > 0 {
		b.WriteString("# Layer environment variables\n")
	}

	// Sort keys for deterministic output (prevents Docker cache invalidation)
	keys := make([]string, 0, len(expanded.Vars))
	for key := range expanded.Vars {
		keys = append(keys, key)
	}
	sortStrings(keys)
	for _, key := range keys {
		// Docker substitutes ${VAR} and $VAR in ENV values at build time
		// using build args + previously-set ENVs. Candy-author-supplied
		// values that reference RUNTIME-injected vars (e.g. POSTGRES_PASSWORD
		// from a podman secret) would be silently emptied. Escape `$` so
		// the references survive verbatim into the runtime container, where
		// shell / *_CMD-style invocations resolve them properly.
		// Build-arg-style refs (TARGETARCH, ARCH) aren't used in env: blocks
		// — they're handled via ARG/ENV pairs inserted by emitVarsEnv —
		// so blanket-escaping `$` is safe here.
		fmt.Fprintf(b, "ENV %s=\"%s\"\n", key, escapeContainerfileEnvValue(expanded.Vars[key]))
	}

	// Append to PATH if there are path additions
	if len(expanded.PathAppend) > 0 {
		pathAdditions := strings.Join(expanded.PathAppend, ":")
		fmt.Fprintf(b, "ENV PATH=\"%s:${PATH}\"\n", pathAdditions)
	}

	if len(expanded.Vars) > 0 || len(expanded.PathAppend) > 0 {
		b.WriteString("\n")
	}
}

// writeExpose emits EXPOSE directives for the box's inherited candy ports —
// the SAME set baked into the ai.opencharly.port label (CollectBoxPorts), so
// EXPOSE and the label can never diverge. CollectBoxPorts already dedups by
// container port and sorts ascending.
func (g *Generator) writeExpose(b *strings.Builder, boxName string) {
	ports, _ := CollectBoxPorts(g.Config, g.Candies, boxName)
	if len(ports) == 0 {
		return
	}
	b.WriteString("# Exposed ports\n")
	for _, port := range ports {
		fmt.Fprintf(b, "EXPOSE %s\n", port)
	}
	b.WriteString("\n")
}

// writeDataStaging emits COPY instructions for data candies into /data/<volume>/[dest/].
// Data files are staged in the image for deploy-time provisioning by charly config / charly update.
func (g *Generator) writeDataStaging(b *strings.Builder, candyOrder []string, img *ResolvedBox) {
	hasData := false
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if !layer.HasData() {
			continue
		}
		if !hasData {
			b.WriteString("# Data staging (for deploy-time provisioning into bind-backed volumes)\n")
			hasData = true
		}
		for _, d := range layer.Data() {
			// Source: candy scratch stage has the candy dir contents at /
			// so data/notebooks/ in the candy dir becomes /data/notebooks/ in the scratch stage
			srcPath := "/" + d.Src
			if !strings.HasSuffix(srcPath, "/") {
				srcPath += "/"
			}

			// Destination: /data/<volume>/[dest/]
			dstPath := "/data/" + d.Volume + "/"
			if d.Dest != "" {
				dstPath += d.Dest
				if !strings.HasSuffix(dstPath, "/") {
					dstPath += "/"
				}
			}

			// Use the short stage alias (layer.Name) to match `FROM scratch AS
			// <layer.Name>` — for REMOTE candies candyName is the full @github map
			// key, which is NOT a valid build-stage reference (podman would try to
			// pull it as an image). Local candies: candyName == layer.Name (no-op).
			fmt.Fprintf(b, "COPY --from=%s --chown=%d:%d %s %s\n",
				layer.Name, img.UID, img.GID, srcPath, dstPath)
		}
	}
	if hasData {
		b.WriteString("\n")
	}
}

// generateTraefikRoutes generates a traefik dynamic config YAML for route candies
func (g *Generator) generateTraefikRoutes(boxName string, candyOrder []string, _ *ResolvedBox) error {
	var b strings.Builder

	b.WriteString("# .build/" + boxName + "/traefik-routes.yml (generated -- do not edit)\n")
	b.WriteString("http:\n")
	b.WriteString("  routers:\n")

	// Collect routes in candy order (deterministic)
	type routeEntry struct {
		name string
		cfg  *RouteConfig
	}
	var routes []routeEntry
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if !layer.HasRoute() {
			continue
		}
		route, err := layer.Route()
		if err != nil || route == nil {
			continue
		}
		routes = append(routes, routeEntry{name: candyName, cfg: route})
	}

	for _, r := range routes {
		// Schema v4: DNS removed from ResolvedBox (deploy-only choice).
		// Traefik route hostnames come from the candy's host declaration.
		// Deploy-time DNS override via BundleNode.DNS applies separately.
		host := r.cfg.Host

		fmt.Fprintf(&b, "    %s:\n", r.name)
		fmt.Fprintf(&b, "      rule: \"Host(`%s`)\"\n", host)
		fmt.Fprintf(&b, "      service: %s\n", r.name)
		b.WriteString("      entryPoints:\n")
		b.WriteString("        - websecure\n")
		b.WriteString("      tls:\n")
		b.WriteString("        certResolver: letsencrypt\n")
	}

	b.WriteString("  services:\n")
	for _, r := range routes {
		fmt.Fprintf(&b, "    %s:\n", r.name)
		b.WriteString("      loadBalancer:\n")
		b.WriteString("        servers:\n")
		fmt.Fprintf(&b, "          - url: \"http://127.0.0.1:%s\"\n", r.cfg.Port)
	}

	imageDir := filepath.Join(g.BuildDir, boxName)
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		return err
	}

	routesYAML := []byte(b.String())
	// Egress gate: the hand-built traefik dynamic config must validate before it
	// is written into the build context (see /charly-internals:egress).
	if err := ValidateEgress("traefik_routes", filepath.Join(boxName, "traefik-routes.yml"), routesYAML); err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(imageDir, "traefik-routes.yml"), routesYAML, 0644)
}

// generateInitFragments writes init system config fragments to
// .build/<image>/<fragmentDir>/. Schema-driven: iterates each candy's
// service: list and renders every entry that binds to this init via
// per-entry routing (use_packaged → systemd; custom exec → any init with
// a service_template). No legacy raw-INI path.
func (g *Generator) generateInitFragments(boxName, initName string, def *InitDef, candyOrder []string) error {
	fragDir := filepath.Join(g.BuildDir, boxName, def.FragmentDir)
	if err := os.MkdirAll(fragDir, 0755); err != nil {
		return err
	}
	img := g.Boxes[boxName]

	for i, candyName := range candyOrder {
		layer := g.Candies[candyName]
		idx := i + 1

		if def.Model == "fragment_assembly" {
			// Concatenate every service entry in this candy that binds to this init
			// into ONE fragment file per candy, matching the Containerfile's
			// stage_fragment_copy naming convention (NN-<candy>.conf).
			var candyBuf strings.Builder
			for j := range layer.Service() {
				entry := &layer.Service()[j]
				// Per-distro filter: skip entries whose distro: list excludes
				// this box's distro (the modular virtqemud/virtnetworkd vs
				// monolithic libvirtd split — see serviceEntryAppliesToDistro).
				if img != nil && !serviceEntryAppliesToDistro(entry, img.Distro) {
					continue
				}
				// Per-entry routing: only render entries this init can handle.
				if entry.IsPackaged() {
					if def.ServiceSchema == nil || !def.ServiceSchema.SupportsPackaged {
						continue
					}
				} else {
					if def.ServiceSchema == nil || def.ServiceSchema.ServiceTemplate == "" {
						continue
					}
				}
				ctx := ServiceRenderContext{
					Name:             entry.Name,
					Candy:            candyName,
					Exec:             entry.Exec,
					Env:              entry.Env,
					EnvList:          mapToKeyValueSlice(entry.Env),
					Restart:          entry.Restart,
					WorkingDirectory: entry.WorkingDirectory,
					User:             entry.User,
					After:            entry.After,
					Before:           entry.Before,
					Stdout:           entry.Stdout,
					StopTimeout:      entry.StopTimeout,
					Scope:            entry.EffectiveScope(),
				}
				rendered, err := RenderService(entry, def, ctx)
				if err != nil {
					return fmt.Errorf("rendering service %s/%s/%s: %w", initName, candyName, entry.Name, err)
				}
				content := rendered.UnitText
				if content == "" {
					content = rendered.DropinText
				}
				if content == "" {
					continue
				}
				if candyBuf.Len() > 0 && !strings.HasSuffix(candyBuf.String(), "\n\n") {
					if !strings.HasSuffix(candyBuf.String(), "\n") {
						candyBuf.WriteString("\n")
					}
					candyBuf.WriteString("\n")
				}
				candyBuf.WriteString(content)
				if !strings.HasSuffix(content, "\n") {
					candyBuf.WriteString("\n")
				}
			}
			if candyBuf.Len() > 0 {
				// Short name, not the slashed remote map key (see scratch-stage note).
				fragFile := filepath.Join(fragDir, fmt.Sprintf("%02d-%s.conf", idx, layer.Name))
				if err := atomicWriteFile(fragFile, []byte(candyBuf.String()), 0644); err != nil {
					return err
				}
			}
		}

		// Port relay fragments (unchanged — use candy position in filename to
		// match Containerfile's stage_fragment_copy naming).
		if initHasRelayTemplate(def) && len(layer.PortRelayPorts) > 0 {
			for _, port := range layer.PortRelayPorts {
				content, err := initRenderRelayTemplate(def, port, candyName, idx)
				if err != nil {
					return fmt.Errorf("rendering relay for %s/%s port %d: %w", initName, candyName, port, err)
				}
				confName := fmt.Sprintf("%02d-relay-%d.conf", idx, port)
				fragFile := filepath.Join(fragDir, confName)
				if err := atomicWriteFile(fragFile, []byte(content), 0644); err != nil {
					return err
				}
			}
		}

		// File copy model: copy detected service files (systemd *.service globs).
		if def.Model == "file_copy" {
			for _, svcPath := range layer.ServiceFiles() {
				content, err := os.ReadFile(svcPath)
				if err != nil {
					return fmt.Errorf("reading service file %s: %w", svcPath, err)
				}
				destFile := filepath.Join(fragDir, filepath.Base(svcPath))
				if err := atomicWriteFile(destFile, content, 0644); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// mapToKeyValueSlice deterministically sorts a map into []KeyValue for
// template iteration. Matches the existing ServiceRenderContext contract.
func mapToKeyValueSlice(m map[string]string) []KeyValue {
	if len(m) == 0 {
		return nil
	}
	out := make([]KeyValue, 0, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, KeyValue{Key: k, Value: m[k]})
	}
	return out
}

// writeCandySteps writes the RUN steps for a single candy.
// skipRootReset prevents emitting USER root after user-mode steps (used for the
// last candy when no post-candy root steps follow).
// Returns true if the candy ended in user mode.
//
//nolint:gocyclo // candy-build step sequence (ENV/packages/localpkg/tasks/builders/shell/user-reset) sharing asUser state; conditionally ordered, cohesive
func (g *Generator) writeCandySteps(b *strings.Builder, candyName string, img *ResolvedBox, skipRootReset bool) (bool, error) {
	layer := g.Candies[candyName]
	stageName := layer.Name // short name used as scratch stage alias

	fmt.Fprintf(b, "# Layer: %s\n", candyName)

	// Track if we've switched to user mode
	asUser := false

	// 0. ENV from vars: + ARCH (ARG TARGETARCH) — emitted once per candy
	// before packages/tasks so Docker's variable substitution sees the
	// values in subsequent directives (COPY dests, RUN commands, etc.).
	if layer.HasTasks() || len(layer.vars) > 0 {
		emitVarsEnv(b, layer.vars)
	}

	// 1. System packages — resolved by THE shared distro-specificity cascade
	// (resolveCascadePackages — the SAME resolver the deploy path uses, so build
	// and deploy can never diverge). It folds the candy's top-level `package:`
	// base, unions every matching distro tag section over img.Distro, and
	// resolves repo/options/etc. most-specific-wins. Emits the PRIMARY format
	// (img.Pkg) install; non-primary build formats (the `aur` builder) emit from
	// their own format section below.
	if pkgs, raw, matched := resolveCascadePackages(layer, img); (matched || len(pkgs) > 0) && img.DistroDef != nil {
		if formatDef := img.DistroDef.Format[img.Pkg]; formatDef != nil {
			ctx := NewInstallContext(raw, formatDef.CacheMount)
			if rendered, err := RenderTemplate(img.Pkg+"-install", formatDef.InstallTemplate, ctx); err == nil {
				b.WriteString(rendered)
			}
		}
	}

	// Non-primary build formats (e.g. `aur` for build: [pac, aur]) are secondary
	// BUILD formats consumed by a multi-stage builder, NOT distro package tags, so
	// they emit from their own format section (the cascade above owns img.Pkg).
	for _, format := range img.BuildFormats {
		if format == img.Pkg {
			continue // primary format handled by the cascade above
		}
		section := layer.FormatSection(format)
		if section == nil || len(section.Packages) == 0 {
			continue
		}
		if img.DistroDef == nil || img.DistroDef.Format == nil {
			continue
		}
		formatDef := img.DistroDef.Format[format]
		if formatDef == nil {
			continue
		}
		if builderDef, ok := img.BuilderConfig.Builder[format]; ok && !builderDef.Inline {
			// Format with builder: use the format's install_template (e.g., aur COPY + pacman -U)
			ctx := &InstallContext{
				CacheMounts: formatDef.CacheMount,
				Packages:    section.Packages,
				StageName:   fmt.Sprintf("%s-%s-build", layer.Name, format),
			}
			if rendered, err := RenderTemplate(format+"-install", formatDef.InstallTemplate, ctx); err == nil {
				b.WriteString(rendered)
			}
		} else {
			ctx := NewInstallContext(section.Raw, formatDef.CacheMount)
			if rendered, err := RenderTemplate(format+"-install", formatDef.InstallTemplate, ctx); err == nil {
				b.WriteString(rendered)
			}
		}
	}

	// 2.5 localpkg: install the candy's OS package in the IMAGE build — the same
	// dep-resolving install the deploy LocalPkgInstallStep performs, so a localpkg
	// candy (the `charly` toolchain) installs as a proper, OS-tracked,
	// dependency-resolving package on EVERY distro image, not a curl'd raw binary.
	// compileLocalPkgStep resolves the target distro's localpkg-capable format and
	// the candy's source; renderLocalPkgImageInstall (shared with OCITarget — R3)
	// then picks the binary source by box type: a PRODUCTION box downloads the
	// PUBLISHED release; a DISPOSABLE check bed (g.DevLocalPkg) builds the
	// IN-DEVELOPMENT package from local source. Emits "" when the format declares
	// no localpkg contract (the candy's own task: install is the fallback).
	if step := compileLocalPkgStep(layer, img, HostContext{}); step != nil {
		if s, ok := step.(*LocalPkgInstallStep); ok {
			run, err := renderLocalPkgImageInstall(s, g.DevLocalPkg, filepath.Join(g.BuildDir, img.Name), img.Name)
			if err != nil {
				fmt.Fprintf(b, "# localpkg render error: %v\n", err)
			} else {
				b.WriteString(run)
			}
		}
	}

	// 2a. tasks: list (new path — replaces both root.yml and user.yml).
	// Validator rejects candies that have both tasks: and root.yml/user.yml.
	if layer.HasTasks() {
		boxName := img.Name
		buildDir := filepath.Join(g.BuildDir, boxName)
		contextRelPrefix := filepath.ToSlash(filepath.Join(".build", boxName))
		finalUser, err := g.emitTasks(b, layer, img, layer.runOps(), buildDir, contextRelPrefix)
		if err != nil {
			// Phase 0: log but continue; validator should catch this earlier.
			fmt.Fprintf(b, "# emitTasks error: %v\n", err)
		}
		if finalUser != "0" && finalUser != "root" {
			// Tasks ended in non-root state; reset for builders/user.yml that
			// follow in existing code paths (they assume USER=root at entry).
			b.WriteString("USER root\n")
		}
	}

	// 4. Inline builders (cargo, etc.) — an EXTERNALIZED inline builder (cargo) renders its
	// in-candy RUN via its plugin's OpResolve build leg (C10, InlineFragment); a custom
	// (non-externalized) inline builder renders from the embedded builder: vocabulary
	// install_template.
	if img.BuilderConfig != nil {
		for _, bName := range img.BuilderConfig.BuilderNames() {
			bDef := img.BuilderConfig.Builder[bName]
			if !bDef.Inline {
				continue
			}
			external := externalizedBuilders[bName]
			if !external && bDef.InstallTemplate == "" {
				continue
			}
			if !g.candyNeedsBuilder(img, layer, bDef) {
				continue
			}
			if !asUser {
				fmt.Fprintf(b, "USER %d\n", img.UID)
				asUser = true
			}
			ctx := &BuildStageContext{
				LayerStage:  stageName,
				UID:         img.UID,
				GID:         img.GID,
				CacheMounts: bDef.CacheMount,
			}
			if external {
				if err := ensureBuildersConnected(context.Background(), g.Config, g.Dir, []string{bName}); err != nil {
					return asUser, fmt.Errorf("candy %q: connect inline builder %q: %w", candyName, bName, err)
				}
				prov, ok := providerRegistry.ResolveBuilder(bName)
				if !ok {
					return asUser, fmt.Errorf("candy %q: inline builder %q is externalized but its plugin is not connected", candyName, bName)
				}
				in := builderResolveInputFrom(layer.Name, bName, bDef, ctx)
				reply, err := resolveBuilderStage(prov, bName, in, img)
				if err != nil {
					return asUser, fmt.Errorf("candy %q: inline builder %q resolve: %w", candyName, bName, err)
				}
				if strings.TrimSpace(reply.InlineFragment) == "" {
					return asUser, fmt.Errorf("candy %q: inline builder %q returned an empty OpResolve inline fragment", candyName, bName)
				}
				b.WriteString(reply.InlineFragment)
				continue
			}
			rendered, err := RenderTemplate(bName+"-inline", bDef.InstallTemplate, ctx)
			if err != nil {
				return asUser, fmt.Errorf("candy %q: rendering inline builder %q: %w", candyName, bName, err)
			}
			b.WriteString(rendered)
		}
	}

	// 5. Shell-init snippets from the candy manifest `shell:` block.
	// Reuses the InstallPlan compiler so the box-build generator and the
	// pod-overlay path share one source of truth for selection-rule +
	// destination resolution. We emit the resulting steps inline as
	// RUN-heredoc directives (parallel to how candy/plugin-installstep's
	// shell-snippet OpEmit renders the pod-overlay fragment — same
	// sha256-derived end-marker).
	if shellSteps := compileShellSnippetSteps(layer, img, HostContext{}); len(shellSteps) > 0 {
		// Shell snippets are root-owned system drop-ins; reset to root
		// before emission so RUN runs as root.
		if asUser {
			b.WriteString("USER root\n")
			asUser = false
		}
		for _, step := range shellSteps {
			s, ok := step.(*ShellSnippetStep)
			if !ok || s == nil || s.Snippet == "" {
				continue
			}
			h := sha256.Sum256([]byte(s.Snippet))
			marker := fmt.Sprintf("CHARLY_SHELL_%s_%x", strings.ToUpper(s.Shell), h[:4])
			fmt.Fprintf(b,
				"RUN mkdir -p %s && cat > %s <<'%s'\n%s\n%s\n",
				shellQuote(filepath.Dir(s.Destination)),
				shellQuote(s.Destination),
				marker,
				s.Snippet,
				marker,
			)
		}
	}

	// Reset to root for next candy (skip for last candy when no root steps follow)
	if asUser && !skipRootReset {
		b.WriteString("USER root\n")
	}

	b.WriteString("\n")
	return asUser, nil
}

// Old format-specific write functions removed — all generation is now
// config-driven via the embedded distro: vocabulary format templates rendered by renderFormatInstall*
// and the embedded builder: vocabulary templates rendered by buildStageContext + RenderTemplate.

// expandBuilderPath replaces {{.Home}} placeholders in copy artifact paths.
func expandBuilderPath(path string, img *ResolvedBox) string {
	path = strings.ReplaceAll(path, "{{.Home}}", img.Home)
	return path
}

// candyNeedsBuilder checks if a candy triggers a builder's detection criteria.
func (g *Generator) candyNeedsBuilder(img *ResolvedBox, layer *Candy, builderDef *BuilderDef) bool {
	for _, f := range builderDef.DetectFiles {
		if candyHasFile(layer, f) {
			return true
		}
	}
	if builderDef.DetectConfig != "" {
		section := layer.FormatSection(builderDef.DetectConfig)
		if section != nil && len(section.Packages) > 0 {
			// Distro-aware gate: a config-only detection (no DetectFiles
			// match) means the builder is tightly coupled to a specific
			// distro's install_template (e.g. arch.formats.aur runs
			// `pacman -U`). The IR compiler (install_build.go:236-249)
			// only emits install steps for formats in img.BuildFormats —
			// so when the image's build modes don't include this format,
			// the section is unreachable and the builder is not needed.
			// Multi-distro candies can carry rpm: + pac: + aur: together
			// without forcing every Fedora consumer to invoke
			// arch-builder. Mirrors the validate.go validateBuilders
			// gate (Section K-1).
			if img != nil && !buildFormatsInclude(img.BuildFormats, builderDef.DetectConfig) {
				return false
			}
			return true
		}
	}
	return false
}

// buildFormatsInclude returns true if formats contains target.
func buildFormatsInclude(formats []string, target string) bool {
	return slices.Contains(formats, target)
}

// collectBuilderRuntimeEnv returns synthesised EnvConfig entries for every
// builder triggered by any candy in candyOrder. Each builder contributes at
// most one config — the same builder is not double-counted even when many
// candies trigger it. Used by writeCandyEnv (Containerfile ENV emission) and
// writeLabels (ai.opencharly.path_append + ai.opencharly.env_candy).
//
// The "runtime env contract" idea moved from the pixi CANDY to the pixi
// BUILDER in 2026-04-29 because candy-level env: + path_append: only
// reached an image when (a) the candy was a top-level entry on the image
// or (b) sibling-grouped auto-intermediate inheritance carried it through.
// Images using pixi via pixi.toml-triggered builds (jupyter, openwebui,
// immich-ml, …) had neither, and ended up with /usr/local/bin:/usr/bin
// at runtime — supervisord couldn't find `jupyter` in PATH, even though
// the binary lived at ~/.pixi/envs/default/bin/jupyter. Threading via the
// BUILDER means any image whose candies trigger pixi gets the contract
// automatically.
func (g *Generator) collectBuilderRuntimeEnv(candyOrder []string, img *ResolvedBox) []*EnvConfig {
	if img == nil || img.BuilderConfig == nil {
		return nil
	}
	var out []*EnvConfig
	for _, builderName := range img.BuilderConfig.BuilderNames() {
		def := img.BuilderConfig.Builder[builderName]
		if def == nil {
			continue
		}
		if len(def.RuntimeEnv) == 0 && len(def.PathContributions) == 0 {
			continue
		}
		triggered := false
		for _, candyName := range candyOrder {
			layer, ok := g.Candies[candyName]
			if !ok {
				continue
			}
			if g.candyNeedsBuilder(img, layer, def) {
				triggered = true
				break
			}
		}
		if !triggered {
			continue
		}
		out = append(out, &EnvConfig{
			Vars:       def.RuntimeEnv,
			PathAppend: def.PathContributions,
		})
	}
	return out
}

// buildStageContext creates the template context for a builder's stage_template.
func (g *Generator) buildStageContext(layer *Candy, builderName string, builderDef *BuilderDef, img *ResolvedBox, builderRef string) *BuildStageContext {
	stageName := fmt.Sprintf("%s-%s-build", layer.Name, builderName)
	ctx := &BuildStageContext{
		BuilderRef:  builderRef,
		StageName:   stageName,
		LayerStage:  layer.Name,
		CopySrc:     g.candyCopySource(candyMapKey(layer)),
		UID:         img.UID,
		GID:         img.GID,
		Home:        img.Home,
		User:        img.User,
		CacheMounts: builderDef.CacheMount,
	}

	// Resolve manifest and install command for file-detected builders (pixi)
	if len(builderDef.InstallCommands) > 0 && len(builderDef.DetectFiles) > 0 {
		manifest := ""
		for _, f := range builderDef.DetectFiles {
			if candyHasFile(layer, f) {
				manifest = f
				break
			}
		}
		ctx.Manifest = manifest
		ctx.HasLockFile = fileExists(filepath.Join(layer.SourceDir, manifest+".lock")) ||
			(manifest == "pixi.toml" && layer.HasPixiLock)

		// Select install command from builder config
		if ctx.HasLockFile {
			if cmd, ok := builderDef.InstallCommands[manifest+"+lock"]; ok {
				ctx.InstallCmd = cmd
			}
		}
		if ctx.InstallCmd == "" {
			if cmd, ok := builderDef.InstallCommands[manifest]; ok {
				ctx.InstallCmd = cmd
			}
		}
	}

	// Resolve manylinux fix template
	if builderDef.ManylinuxFix != "" && ctx.Manifest != "" {
		rendered, err := RenderTemplate(builderName+"-manylinux", builderDef.ManylinuxFix, ctx)
		if err == nil {
			ctx.ManylinuxFix = rendered
		}
	}

	// Detect optional build script (runs in builder stage after install)
	if builderDef.BuildScript != "" && candyHasFile(layer, builderDef.BuildScript) {
		ctx.HasBuildScript = true
		ctx.BuildScript = builderDef.BuildScript
	}

	// For config-detected builders (aur), extract packages/options from candy config
	if builderDef.DetectConfig != "" {
		section := layer.FormatSection(builderDef.DetectConfig)
		if section != nil {
			ctx.Packages = section.Packages
			ctx.Options = toStringSlice(section.Raw["options"])
		}
	}

	return ctx
}

// writeLabels emits OCI LABEL directives with all runtime-relevant metadata.
// Every runtime config option is embedded so images are fully self-contained.
//
//nolint:gocyclo // ~40 OCI label groups emitted sequentially from image capabilities/metadata; extracting each shares identical (b,g,img,candyOrder) params and harms readability
func (g *Generator) writeLabels(b *strings.Builder, boxName string, candyOrder []string, img *ResolvedBox) {
	b.WriteString("# Image metadata\n")

	// Always-present labels. ai.opencharly.version carries the image's
	// content-derived EffectiveVersion (its dedicated version:, else the highest
	// candy version across the base chain) — NOT the per-build tag. It is stable
	// across builds when no candy changed, so a child's FROM <base> SHA doesn't
	// shift and cache-misses don't cascade. See effective_version.go. Resolution
	// prefers this label over the tag (local_image.go); it is also the
	// "is this an charly box?" presence sentinel (ExtractMetadata).
	fmt.Fprintf(b, "LABEL %s=%q\n", LabelVersion, img.EffectiveVersion)
	fmt.Fprintf(b, "LABEL %s=%q\n", LabelBox, boxName)
	fmt.Fprintf(b, "LABEL %s=%q\n", LabelUID, strconv.Itoa(img.UID))
	fmt.Fprintf(b, "LABEL %s=%q\n", LabelGID, strconv.Itoa(img.GID))
	fmt.Fprintf(b, "LABEL %s=%q\n", LabelUser, img.User)
	fmt.Fprintf(b, "LABEL %s=%q\n", LabelHome, img.Home)

	// Conditional string labels (omitted when empty)
	if img.Registry != "" {
		fmt.Fprintf(b, "LABEL %s=%q\n", LabelRegistry, img.Registry)
	}
	// Bootc-flavored compositions emit the internal round-trip label so
	// deploy-time consumers (labels.go ExtractMetadata) continue to see
	// meta.Bootc=true. The signal is candy-derived now (preserve_user)
	// rather than img.Bootc.
	if img.CandyCaps != nil && img.CandyCaps.PreserveUser {
		fmt.Fprintf(b, "LABEL %s=%q\n", LabelBootc, "true")
	}
	// Candy-contributed OCI labels (capabilities.oci_labels). Includes
	// dev.containers.bootc=true emitted from the bootc-config candy when its
	// preserve_user capability is in the composition. Sorted for
	// determinism so Containerfile diffs stay stable.
	if img.CandyCaps != nil && len(img.CandyCaps.OCILabels) > 0 {
		keys := make([]string, 0, len(img.CandyCaps.OCILabels))
		for k := range img.CandyCaps.OCILabels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(b, "LABEL %s=%q\n", k, img.CandyCaps.OCILabels[k])
		}
	}
	if img.Network != "" {
		fmt.Fprintf(b, "LABEL %s=%q\n", LabelNetwork, img.Network)
	}
	// Schema v4: LabelEngine / LabelDNS / LabelAcmeEmail removed —
	// deployment choices, not image declarations. Deploy-time values
	// flow through BundleNode → BoxMetadata.

	// Platform identity + builder-pool coordination labels.
	// No serialized selector union — derive as ["all"] ∪ distro ∪ formats at read time.
	writeJSONLabel(b, LabelPlatformDistro, img.Distro)
	writeJSONLabel(b, LabelPlatformFormat, img.BuildFormats)
	if len(img.Builder) > 0 {
		writeJSONLabel(b, LabelBuilderUse, map[string]string(img.Builder))
	}
	writeJSONLabel(b, LabelBuilderProvide, img.BuilderCapabilities)

	// JSON array labels (omitted when empty). Ports are inherited from the
	// candy chain (CollectBoxPorts) — boxes no longer declare ports. The label
	// carries bare container ports; the host mapping is resolved at deploy time
	// (auto-allocated on 127.0.0.1, or pinned by a deploy `port:` entry).
	boxPorts, _ := CollectBoxPorts(g.Config, g.Candies, boxName)
	writeJSONLabel(b, LabelPort, boxPorts)

	// Port protocols: collect from candy PortSpec declarations
	portProtos := make(map[string]string)
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		for _, ps := range layer.PortSpecs() {
			if ps.Protocol != "" && ps.Protocol != "http" {
				portProtos[strconv.Itoa(ps.Port)] = ps.Protocol
			}
		}
	}
	writeJSONLabel(b, LabelPortProto, portProtos)

	// Volumes: short form names (without charly-<image>- prefix)
	volumes, _ := CollectBoxVolume(g.Config, g.Candies, boxName, img.Home, nil)
	if len(volumes) > 0 {
		labelVols := make([]LabelVolumeEntry, 0, len(volumes))
		for _, v := range volumes {
			shortName := strings.TrimPrefix(v.VolumeName, "charly-"+boxName+"-")
			labelVols = append(labelVols, LabelVolumeEntry{Name: shortName, Path: v.ContainerPath})
		}
		writeJSONLabel(b, LabelVolume, labelVols)
	}

	// Aliases: collected from candies + image-level config
	aliases, _ := CollectBoxAlias(g.Config, g.Candies, boxName)
	writeJSONLabel(b, LabelAlias, aliases)

	// Security: collected from candies + image config
	security := CollectSecurity(g.Config, g.Candies, boxName)
	if security.Privileged || security.CgroupNS != "" || len(security.CapAdd) > 0 || len(security.Devices) > 0 || len(security.SecurityOpt) > 0 || len(security.GroupAdd) > 0 || security.ShmSize != "" || len(security.Mounts) > 0 {
		writeJSONLabel(b, LabelSecurity, security)
	}

	// Tunnel config is a deploy-time concern — not written to image labels.
	// Managed via charly.yml only (charly config setup).

	// Image-level env vars
	imgCfg := g.Config.Box[boxName]
	writeJSONLabel(b, LabelEnv, imgCfg.Env)

	// Hooks: collected from candies
	hooks := CollectHooks(g.Config, g.Candies, boxName)
	if hooks != nil {
		writeJSONLabel(b, LabelHook, hooks)
	}

	// Description: three-section plan-shaped self-description.
	// Replaces the retired LabelInfo/LabelStatus scalar labels. Local
	// charly.yml `description:` overlays merge at runtime via
	// MergeDeployDescriptions, not here.
	descriptions := CollectDescriptions(g.Config, g.Candies, boxName)
	if descriptions != nil {
		writeJSONLabel(b, LabelDescription, descriptions)
	}

	// Shell-init manifest: three-section JSON of per-(origin, shell)
	// contributions. Candy entries come from each candy's `shell:`
	// block (resolved via the selection rule); box entries from the
	// box-level `shell:` (when present). Deploy-scope defaults baked
	// here; local charly.yml `shell:` overlays merge at deploy time
	// via MergeDeployShell.
	shellSet := CollectShell(g.Config, g.Candies, boxName)
	if shellSet != nil && (len(shellSet.Candy) > 0 || len(shellSet.Box) > 0 || len(shellSet.Deploy) > 0) {
		writeJSONLabel(b, LabelShell, shellSet)
	}

	// VM config + libvirt snippets labels removed in the hard-cutover.
	// VM definitions live in vm.yml (`kind: vm` entities) as a
	// separate artifact from the container image; container image OCI
	// labels no longer describe VM boot parameters.

	// Init system label: active init system name + per-init service list
	if img.InitConfig != nil {
		labelInitSystem, labelInitDef := img.InitConfig.ResolveInitSystem(g.Candies, candyOrder, "")
		if labelInitSystem != "" && labelInitDef != nil {
			fmt.Fprintf(b, "LABEL %s=%q\n", LabelInit, labelInitSystem)
			// Init definition: bake the runtime-relevant subset of the
			// build-resolved init def so deploy reads the entrypoint +
			// management surface from the image instead of a hardcoded
			// registry. Makes the init system TRUE single-source — init
			// systems declared only in the embedded init: vocabulary now
			// reach runtime through this label.
			writeJSONLabel(b, LabelInitDef, CapabilityInitDef{
				Entrypoint:         labelInitDef.Entrypoint,
				FallbackEntrypoint: labelInitDef.FallbackEntrypoint,
				ManagementTool:     labelInitDef.ManagementTool,
				ManagementCommands: labelInitDef.ManagementCommands,
			})
			// Per-init service-name list (legacy candy-name summary; kept for
			// `charly service status/restart` CLI ergonomics).
			var serviceNames []string
			for _, candyName := range candyOrder {
				layer := g.Candies[candyName]
				if layer.HasInit(labelInitSystem) {
					serviceNames = append(serviceNames, candyName)
				}
			}
			if labelInitDef.LabelKey != "" {
				writeJSONLabel(b, labelInitDef.LabelKey, serviceNames)
			}
			// Structured per-entry service spec — source-less deploy reads
			// this instead of relying on charly.yml access at deploy time.
			var capServices []CapabilityService
			for _, candyName := range candyOrder {
				layer := g.Candies[candyName]
				for i := range layer.Service() {
					e := &layer.Service()[i]
					capServices = append(capServices, CapabilityService{
						Name:             e.Name,
						Scope:            e.EffectiveScope(),
						Enable:           e.Enable,
						UsePackaged:      e.UsePackaged,
						Exec:             e.Exec,
						Env:              e.Env,
						Restart:          e.Restart,
						WorkingDirectory: e.WorkingDirectory,
						User:             e.User,
						After:            e.After,
						Before:           e.Before,
						Stdout:           e.Stdout,
						StopTimeout:      e.StopTimeout,
						Kind:             e.Kind,
						Events:           e.Events,
						AutoStart:        e.AutoStart,
						StartRetries:     e.StartRetries,
						StartSec:         e.StartSecs,
						StopSignal:       e.StopSignal,
						ExitCode:         e.ExitCode,
						Priority:         e.Priority,
						Init:             labelInitSystem,
						Candy:            candyName,
					})
				}
			}
			if len(capServices) > 0 {
				writeJSONLabel(b, LabelService, capServices)
			}
		}
	}

	// Port relay: collected from candies
	var portRelay []int
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		portRelay = append(portRelay, layer.PortRelayPorts...)
	}
	writeJSONLabel(b, LabelPortRelay, portRelay)

	// Secrets: collected from candies (metadata only, no values)
	// Deduplicate by name+env composite key: same podman secret can inject into multiple env vars.
	var labelSecrets []LabelSecretEntry
	secretSeen := make(map[string]bool)
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		for _, s := range layer.Secret() {
			key := s.Name + ":" + s.Env
			if secretSeen[key] {
				continue
			}
			secretSeen[key] = true
			target := s.Target
			if target == "" {
				target = "/run/secrets/" + s.Name
			}
			labelSecrets = append(labelSecrets, LabelSecretEntry{
				Name:   s.Name,
				Target: target,
				Env:    s.Env,
			})
		}
	}
	if len(labelSecrets) > 0 {
		writeJSONLabel(b, LabelSecret, labelSecrets)
	}

	// Env provides: env vars provided to other containers (service discovery)
	envProvides := make(map[string]string)
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.HasEnvProvides() {
			maps.Copy(envProvides, layer.EnvProvides())
		}
	}
	if len(envProvides) > 0 {
		writeJSONLabel(b, LabelEnvProvide, envProvides)
	}

	// Env requires: env vars image must have from the environment
	envRequiresMap := make(map[string]EnvDependency) // deduplicate by name, last wins
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.HasEnvRequires() {
			for _, dep := range layer.EnvRequire() {
				envRequiresMap[dep.Name] = dep
			}
		}
	}
	if len(envRequiresMap) > 0 {
		writeJSONLabel(b, LabelEnvRequire, sortedEnvDeps(envRequiresMap))
	}

	// Env accepts: env vars image can optionally use
	envAcceptsMap := make(map[string]EnvDependency) // deduplicate by name, last wins
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.HasEnvAccepts() {
			for _, dep := range layer.EnvAccept() {
				envAcceptsMap[dep.Name] = dep
			}
		}
	}
	if len(envAcceptsMap) > 0 {
		writeJSONLabel(b, LabelEnvAccept, sortedEnvDeps(envAcceptsMap))
	}

	// Secret requires: credential-store-backed env vars image must have
	secretRequiresMap := make(map[string]EnvDependency) // deduplicate by name, last wins
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.HasSecretRequires() {
			for _, dep := range layer.SecretRequire() {
				secretRequiresMap[dep.Name] = dep
			}
		}
	}
	if len(secretRequiresMap) > 0 {
		writeJSONLabel(b, LabelSecretRequire, sortedEnvDeps(secretRequiresMap))
	}

	// Secret accepts: credential-store-backed env vars image can optionally use
	secretAcceptsMap := make(map[string]EnvDependency) // deduplicate by name, last wins
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.HasSecretAccepts() {
			for _, dep := range layer.SecretAccept() {
				secretAcceptsMap[dep.Name] = dep
			}
		}
	}
	if len(secretAcceptsMap) > 0 {
		writeJSONLabel(b, LabelSecretAccept, sortedEnvDeps(secretAcceptsMap))
	}

	// MCP provides: MCP servers provided to other containers
	mcpProvidesMap := make(map[string]MCPServerYAML) // deduplicate by name, last wins
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.HasMCPProvides() {
			for _, mcp := range layer.MCPProvide() {
				mcpProvidesMap[mcp.Name] = mcp
			}
		}
	}
	if len(mcpProvidesMap) > 0 {
		// Sort by name for deterministic output
		names := make([]string, 0, len(mcpProvidesMap))
		for name := range mcpProvidesMap {
			names = append(names, name)
		}
		sort.Strings(names)
		mcpProvides := make([]MCPServerYAML, 0, len(names))
		for _, name := range names {
			mcpProvides = append(mcpProvides, mcpProvidesMap[name])
		}
		writeJSONLabel(b, LabelMCPProvide, mcpProvides)
	}

	// MCP requires: MCP servers image must have from the environment
	mcpRequiresMap := make(map[string]EnvDependency) // deduplicate by name, last wins
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.HasMCPRequires() {
			for _, dep := range layer.MCPRequire() {
				mcpRequiresMap[dep.Name] = dep
			}
		}
	}
	if len(mcpRequiresMap) > 0 {
		writeJSONLabel(b, LabelMCPRequire, sortedEnvDeps(mcpRequiresMap))
	}

	// MCP accepts: MCP servers image can optionally use
	mcpAcceptsMap := make(map[string]EnvDependency) // deduplicate by name, last wins
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.HasMCPAccepts() {
			for _, dep := range layer.MCPAccept() {
				mcpAcceptsMap[dep.Name] = dep
			}
		}
	}
	if len(mcpAcceptsMap) > 0 {
		writeJSONLabel(b, LabelMCPAccept, sortedEnvDeps(mcpAcceptsMap))
	}

	// Routes: collected from candies
	var routes []LabelRouteEntry
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.HasRoute() {
			rc, err := layer.Route()
			if err == nil && rc != nil {
				port, _ := strconv.Atoi(rc.Port)
				routes = append(routes, LabelRouteEntry{Host: rc.Host, Port: port})
			}
		}
	}
	writeJSONLabel(b, LabelRoute, routes)

	// Candy env vars: merged from all candies + builder runtime contributions.
	// Both sources funnel into the same OCI labels so deploy-mode consumers
	// of `ai.opencharly.path_append` and `ai.opencharly.env_candy` see
	// the full effective env regardless of whether it came from a candy's
	// `env:`/`path_append:` or a builder's `runtime_env:`/`path_contributions:`.
	var envConfigs []*EnvConfig
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.envConfig != nil {
			envConfigs = append(envConfigs, layer.envConfig)
		}
	}
	envConfigs = append(envConfigs, g.collectBuilderRuntimeEnv(candyOrder, img)...)
	if len(envConfigs) > 0 {
		merged := MergeEnvConfigs(envConfigs)
		if len(merged.Vars) > 0 {
			writeJSONLabel(b, LabelEnvCandy, merged.Vars)
		}
		writeJSONLabel(b, LabelPathAppend, merged.PathAppend)
	}

	// Skills documentation URL
	skillPath := filepath.Join(g.Dir, "plugins", "charly-images", "skills", boxName, "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		skillURL := fmt.Sprintf("https://github.com/opencharly/charly-plugins/blob/main/charly-images/skills/%s/SKILL.md", boxName)
		fmt.Fprintf(b, "LABEL %s=%q\n", LabelSkill, skillURL)
	}

	// Status and info: the box's effective status is the WORST of its own
	// nominal status (boxes author none → the permissive "working" seed, so the
	// candy chain drives the rung) and every candy's authored `status:`
	// (working|testing|broken, default testing). Info is the first line of each
	// entity's plain-string description.
	effectiveStatus := StatusWorking // a box authors no status; the candy chain drives the rung
	var infoParts []string
	if img.Info != "" {
		infoParts = append(infoParts, img.Info)
	}
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		cs := candyStatus(layer)
		effectiveStatus = worstStatus(effectiveStatus, cs)
		if li := descriptionInfo(layer.Description); li != "" && cs != "working" {
			infoParts = append(infoParts, candyName+": "+li)
		}
	}
	resolvedStatus := resolveStatus(effectiveStatus)
	fmt.Fprintf(b, "LABEL %s=%q\n", LabelStatus, resolvedStatus)

	// Acceptance-depth rung — the per-box check_level gating `charly check run
	// <bed>` (see check_level.go). Always emitted (normalized to the default
	// rung) so a bed runner reading labels never sees an empty value.
	fmt.Fprintf(b, "LABEL %s=%q\n", LabelCheckLevel, ResolveCheckLevel(img.CheckLevel))
	if len(infoParts) > 0 {
		// Collapse block-scalar newlines so the value is one valid LABEL line,
		// then single-quote (NOT %q): a description may legitimately mention a
		// ${VAR} (e.g. ${HOST:<subject>}), and the %q double-quoted form
		// lets buildah try to expand it — which fails with "Unsupported modifier"
		// on, e.g., the `<` in ${HOST:<subject>}. shellSingleQuote matches
		// how every JSON label is emitted (no shell/Dockerfile expansion).
		combinedInfo := strings.ReplaceAll(strings.Join(infoParts, "; "), "\n", " ")
		fmt.Fprintf(b, "LABEL %s=%s\n", LabelInfo, shellSingleQuote(combinedInfo))
	}

	// Candy versions: map of candy name -> CalVer for candies with version set
	candyVersions := make(map[string]string)
	for _, candyName := range candyOrder {
		layer := g.Candies[candyName]
		if layer.Version != "" {
			candyVersions[candyName] = layer.Version
		}
	}
	writeJSONLabel(b, LabelCandyVersion, candyVersions)

	// Data entries: staging paths for deploy-time provisioning.
	// Walk the full image chain (like CollectBoxVolume) to include data
	// entries from candies in parent/intermediate images.
	var dataEntries []LabelDataEntry
	seenDataCandies := make(map[string]bool)
	current := boxName
	for {
		imgDef, ok := g.Config.Box[current]
		if !ok {
			break
		}
		resolved, _ := ResolveCandyOrder(imgDef.Candy, g.Candies, nil)
		for _, candyName := range resolved {
			if seenDataCandies[candyName] {
				continue
			}
			seenDataCandies[candyName] = true
			layer, ok := g.Candies[candyName]
			if !ok || !layer.HasData() {
				continue
			}
			for _, d := range layer.Data() {
				staging := "/data/" + d.Volume + "/"
				if d.Dest != "" {
					staging += d.Dest
					if !strings.HasSuffix(staging, "/") {
						staging += "/"
					}
				}
				dataEntries = append(dataEntries, LabelDataEntry{
					Volume:  d.Volume,
					Staging: staging,
					Candy:   candyName,
					Dest:    d.Dest,
				})
			}
		}
		if baseImg, isInternal := g.Config.Box[imgDef.Base]; isInternal && baseImg.IsEnabled() {
			current = imgDef.Base
		} else {
			break
		}
	}
	if len(dataEntries) > 0 {
		writeJSONLabel(b, LabelDataEntries, dataEntries)
	}

	// Data image flag
	imgConfig := g.Config.Box[boxName]
	if imgConfig.DataImage {
		fmt.Fprintf(b, "LABEL %s=%q\n", LabelDataBox, "true")
	}

	b.WriteString("\n")
}

// writeJSONLabel writes a JSON-encoded LABEL directive. Omits the label if the value is nil/empty.
func writeJSONLabel[T any](b *strings.Builder, key string, value T) {
	// Check for nil/empty slices and maps via JSON
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	s := string(data)
	if s == "null" || s == "[]" || s == "{}" {
		return
	}
	// Wrap in single-quoted form with proper '\'' escaping so embedded single
	// quotes (common inside test command strings like awk '{print $1}') don't
	// terminate the LABEL value and trip podman's key=value parser.
	fmt.Fprintf(b, "LABEL %s=%s\n", key, shellSingleQuote(s))
}

// resolveStatus returns the effective status string. Empty defaults to "testing".
// Accepts a single status word (working/testing/broken) — the legacy form
// used by older callers. Prefer resolveStatusFromTags for new code that
// reads from Description.Tag directly.
func resolveStatus(s string) string {
	if s == "" {
		return "testing"
	}
	return s
}

// Status rungs. The default (empty) is "testing"; "working" is the most
// permissive (used as the box-status seed so the candy chain drives the rung).
const (
	StatusWorking = "working"
	StatusTesting = "testing"
	StatusBroken  = "broken"
)

// candyStatus returns a candy's authored maturity rung (working|testing|broken),
// defaulting an unset value to "testing". The authoritative per-candy status
// source — replaces the retired Description.Tag derivation.
func candyStatus(c *Candy) string {
	if c == nil {
		return StatusTesting
	}
	return resolveStatus(c.Status)
}

// descriptionInfo returns the human-facing summary: the FIRST line of the
// plain-string description (multi-line prose lives in the rest of the string).
func descriptionInfo(d string) string {
	d = strings.TrimSpace(d)
	if d == "" {
		return ""
	}
	if before, _, ok := strings.Cut(d, "\n"); ok {
		return strings.TrimSpace(before)
	}
	return d
}

// statusSeverity returns a numeric severity for status comparison.
func statusSeverity(s string) int {
	switch resolveStatus(s) {
	case "working":
		return 0
	case "testing":
		return 1
	case "broken":
		return 2
	default:
		return 1 // unknown treated as testing
	}
}

// worstStatus returns the more severe of two status values.
func worstStatus(a, b string) string {
	if statusSeverity(b) > statusSeverity(a) {
		return resolveStatus(b)
	}
	return resolveStatus(a)
}

// createRemoteCandyCopies copies remote candy directories into versioned
// .build/_candy/<name>.<version>/ dirs
// so that Docker/Podman can access them from the build context.
// Uses hard copies instead of symlinks because Podman doesn't follow symlinks
// that point outside the build context.
func (g *Generator) createRemoteCandyCopies() error {
	hasRemote := false
	for _, layer := range g.Candies {
		if layer.Remote {
			hasRemote = true
			break
		}
	}
	if !hasRemote {
		// No remote candies → no image COPYs from _candy, so any stale _candy
		// is unreferenced and harmless (pruned by `charly clean`). Leave it.
		return nil
	}

	// Each remote candy is staged into its OWN version-keyed dir
	// .build/_candy/<name>.<version>/ — built in a per-process temp then
	// installed via renameat2(RENAME_EXCHANGE). Version-keying keeps DISTINCT
	// candy versions in DISTINCT dirs, so two concurrent builds resolving a
	// candy at different versions never clobber each other (the old shared
	// .build/_layers/<name>/ was last-writer-wins across versions). The atomic
	// install closes the within-version concurrent-COPY race; identical content
	// → identical bytes → podman's cache still hits. `charly clean` prunes
	// outdated <name>.<oldversion> dirs.
	candyRoot := filepath.Join(g.BuildDir, "_candy")
	if err := os.MkdirAll(candyRoot, 0o755); err != nil {
		return err
	}
	for ref, layer := range g.Candies {
		if !layer.Remote {
			continue
		}
		tmp, err := os.MkdirTemp(candyRoot, "."+layer.Name+".tmp.*")
		if err != nil {
			return err
		}
		// Copy the candy's CONTENTS (trailing /.) into the temp so the versioned
		// dir holds the files directly (the Containerfile COPYs `<dir>/ /`).
		cmd := exec.Command("cp", "-a", layer.Path+"/.", tmp)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("copying remote candy %s: %s: %w", ref, string(out), err)
		}
		if err := installDirAtomic(tmp, filepath.Join(candyRoot, candyStageDirName(layer))); err != nil {
			return fmt.Errorf("installing remote candy %s: %w", ref, err)
		}
	}

	return nil
}

// remoteBuildConfigCacheRoot derives the repo cache root that a remotely-included
// build.yml was read from, by stripping the candy subpath off any remote candy's
// cached Path (every remote candy + the remote build.yml share one repo@version
// cache). Returns "" when the build-config is local (no remote candies).
func (g *Generator) remoteBuildConfigCacheRoot() string {
	for _, l := range g.Candies {
		if l.Remote && l.Path != "" {
			suffix := filepath.Join(l.SubPathPrefix, l.Name) // e.g. "candy/pixi"
			if trimmed, ok := strings.CutSuffix(l.Path, suffix); ok {
				return strings.TrimRight(trimmed, string(filepath.Separator))
			}
		}
	}
	return ""
}

// materializeBuildConfigAsset ensures a build-config asset file (referenced by a
// remotely-included build.yml — e.g. the init header_file) is available in the
// build context. If the project ships the file locally (local build.yml), relPath
// is returned unchanged. Otherwise the file is copied from the remote build-config
// cache into .build/_buildconfig/<relPath> (gitignored, like .build/_candy/) and
// the build-root-relative path is returned for use as a COPY source.
func (g *Generator) materializeBuildConfigAsset(relPath string) (string, error) {
	if relPath == "" {
		return relPath, nil
	}
	if _, err := os.Stat(filepath.Join(g.Dir, relPath)); err == nil {
		return relPath, nil // local build-config ships the asset; COPY works as-is
	}
	root := g.remoteBuildConfigCacheRoot()
	if root == "" {
		return relPath, nil // no remote source to pull from; leave as authored
	}
	srcAbs := filepath.Join(root, relPath)
	if _, err := os.Stat(srcAbs); err != nil {
		return relPath, nil // not in the remote cache either; leave as authored
	}
	destAbs := filepath.Join(g.BuildDir, "_buildconfig", relPath)
	if err := os.MkdirAll(filepath.Dir(destAbs), 0755); err != nil {
		return relPath, err
	}
	if out, err := exec.Command("cp", "-a", srcAbs, destAbs).CombinedOutput(); err != nil {
		return relPath, fmt.Errorf("materializing build-config asset %s: %s: %w", relPath, string(out), err)
	}
	return filepath.ToSlash(filepath.Join(".build", "_buildconfig", relPath)), nil
}

// rewriteHeaderCopyForRemote rewrites a `COPY <src> <dst>` header directive so its
// source points at a materialized build-config asset when the original src isn't in
// the local build context. Plain 3-token COPY only; anything else passes through.
func (g *Generator) rewriteHeaderCopyForRemote(headerCopy string) (string, error) {
	fields := strings.Fields(headerCopy)
	if len(fields) != 3 || fields[0] != "COPY" {
		return headerCopy, nil
	}
	newSrc, err := g.materializeBuildConfigAsset(fields[1])
	if err != nil {
		return headerCopy, err
	}
	if newSrc == fields[1] {
		return headerCopy, nil
	}
	return fmt.Sprintf("COPY %s %s", newSrc, fields[2]), nil
}

// candyCopySource returns the COPY source path for a candy in the Containerfile,
// relative to the build context root (g.Dir).
//
// For the common case (no `directory:` override in the candy manifest), this returns
// "candy/<candyRef>/" for local candies and ".build/_candy/<name>.<version>/" for remote
// candies — identical to the legacy behavior.
//
// When the candy declares `directory:` and points SourceDir outside the default
// candy/<name>/ location, the result is the build-root-relative path to
// SourceDir so that Containerfile COPY directives pick up files from the author's
// chosen config directory.
// candyMapKey returns the key under which a candy is stored in g.Candies: the
// fully-qualified remote ref (RepoPath/SubPathPrefix/Name) for remote candies,
// the short name for local ones. Use this whenever code holds a *Candy but
// needs to look it up (or pass its key to candyCopySource), since a remote
// candy's short Name does NOT match its map key.
func candyMapKey(layer *Candy) string {
	if layer.Remote {
		return layer.RepoPath + "/" + layer.SubPathPrefix + layer.Name
	}
	return layer.Name
}

// candyByName resolves a candy by its INTRINSIC bare name against g.Candies.
// It is the FORWARD counterpart of candyMapKey (which maps a *Candy back to its
// store key): a LOCAL candy is keyed bare == Name, so the direct lookup hits; a
// REMOTE candy (e.g. a deploy's add_candy: pulled via ResolveOpts.ExtraCandyRefs)
// is keyed under its fully-qualified ref (candyMapKey), so the direct bare lookup
// MISSES and we fall back to matching the Candy's own Name. Every call site that
// holds a bare candy name (a plan step's CandyName; an overlay-candy name from
// collectOverlayCandies / p.AddCandies) and needs the *Candy goes through here, so
// a remote add_candy overlay layer resolves instead of being silently skipped
// (the add_candy-on-pod-overlay "candy not found" / skipped-stage class).
func (g *Generator) candyByName(name string) *Candy {
	if g == nil {
		return nil
	}
	if c := g.Candies[name]; c != nil {
		return c
	}
	for _, c := range g.Candies {
		if c != nil && c.Name == name {
			return c
		}
	}
	return nil
}

// candyStageDirName is the versioned staging subdir for a remote candy under
// .build/_candy/ — "<name>.<version>". Keying by the candy's CalVer keeps
// DIFFERENT versions of the same candy in DISTINCT dirs, so concurrent builds
// resolving a candy at different versions never clobber each other (the old
// shared .build/_layers/<name>/ was last-writer-wins across versions), and
// `charly clean` can prune outdated versions. Candy names are dot-free
// (lowercase-hyphenated), so the version (a dotted CalVer) parses back off the
// FIRST dot. Cache-safe: the path changes iff the candy version changes.
func candyStageDirName(layer *Candy) string {
	if layer.Version == "" {
		return layer.Name // defensive; remote candies are mandatorily versioned
	}
	return layer.Name + "." + layer.Version
}

func (g *Generator) candyCopySource(candyRef string) string {
	layer := g.Candies[candyRef]
	if layer.Remote {
		return ".build/_candy/" + candyStageDirName(layer)
	}
	// If SourceDir matches the default candy/<candyRef>/ location, preserve
	// the legacy path format (cheap, avoids filepath.Rel calls on hot path).
	defaultDir := filepath.Join(g.Dir, DefaultCandyDir, candyRef)
	if layer.SourceDir == "" || layer.SourceDir == defaultDir {
		return DefaultCandyDir + "/" + candyRef
	}
	// `directory:` override — resolve SourceDir relative to the build root.
	rel, err := filepath.Rel(g.Dir, layer.SourceDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		// Falling back to the default means the Containerfile will miss files
		// from an out-of-tree SourceDir — validation should have caught this.
		return DefaultCandyDir + "/" + candyRef
	}
	return rel
}
