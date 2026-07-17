package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// Generator holds state for generating build artifacts
type Generator struct {
	Dir     string
	Config  *Config
	Candies map[string]spec.CandyReader
	// InitConfig is the project init: vocabulary. Init-system resolution
	// (ActiveInit/ResolveInitSystem) runs over Candies + candyOrder and lives
	// on the Generator — one project init config threaded to the build + pod-
	// overlay emit sites, NOT carried on each ResolvedBox (decoupled in P3).
	InitConfig     *InitConfig
	Tag            string
	Boxes          map[string]*buildkit.ResolvedBox
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
	// a production box build leaves it false. See deploykit.RenderLocalPkgImageInstall.
	DevLocalPkg bool

	// (The per-image builder-reply caches externalBuilderReplies/detectionBuilderReplies
	// moved to the deploykit render Generator with the BUILDER-render engine, K3-A; the
	// dkGen memo below keeps them stable across an image's render methods.)

	// dkGen caches the sdk/deploykit render Generator (P8). Built once by
	// toDeploykit() and reused across an image's render so the deploykit-side
	// per-image builder-reply caches persist across the render methods (which are
	// relocating onto deploykit.Generator). Containerfiles is a shared map ref so
	// writes propagate; Candies/Boxes are stable post-NewGenerator.
	dkGen *deploykit.Generator
}

// globalOrderForBox → deploykit.Generator.GlobalOrderForBox (P8 shim).
func (g *Generator) globalOrderForBox(imageCandies []string, parentCandies map[string]bool) ([]string, error) {
	return g.toDeploykit().GlobalOrderForBox(imageCandies, parentCandies)
}

// resolveUserContext detects existing user in base image or uses configured values
func (g *Generator) resolveUserContext(img *buildkit.ResolvedBox) {
	if !img.IsExternalBase {
		// Internal base - inherit from parent, but respect explicit overrides
		parentImg := g.Boxes[img.Base]
		origCfg, _ := g.Config.BoxConfig(img.Name)

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

	// External base - try to detect existing user at configured UID via verb:oci
	// (the go-containerregistry adopt-user probe lives in candy/plugin-oci now).
	userInfo, err := invokeOciInspectUser(img.Base, img.UID)
	if err != nil {
		// Can't inspect, use configured defaults
		return
	}

	if userInfo.Found {
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

	// InitCfg threads the init-system host-completion pass INTO the scan pipeline (W9): a
	// spec.CandyReader is read-only, so InitSystems must be populated BEFORE ScanAllCandyWithConfigOpts
	// wraps each winning candidate — there is no later separate PopulateCandyInitSystem call anymore.
	opts.InitCfg = defaultInitCfg
	layers, err := ScanAllCandyWithConfigOpts(dir, cfg, opts)
	if err != nil {
		return nil, err
	}

	// Build-time plugin connect (operator-authorized build-time plugin execution).
	// Connect the project's OUT-OF-TREE plugin candies so an external step/builder/verb
	// provider is registered + dialable DURING image generation — the SAME loader the
	// deploy/check paths use (loadDeployPlugins / resolveCheckRunnerContext), transport-
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
	// (C10, kit.BuilderResolve); the deploykit EmitBuilderStages render connects those builder plugins on-demand
	// (ensureBuildersConnected), so they are NOT collected here.
	buildRefs := collectReferencedPluginWords(layers, cfg.Box, nil)
	if perr := loadProjectPlugins(context.Background(), layers, buildRefs); perr != nil {
		fmt.Fprintf(os.Stderr, "warning: build-time plugin load: %v\n", perr)
	}

	// Pre-build validation gate — dispatched to the compiled-in validate capability (candy/plugin-box)
	// by word with a structured OpValidate op (task #60 (C-refined)); the validate ENGINE no longer
	// lives in core. validateProjectForBuild returns the ValidationError-equivalent on any finding.
	if err := validateProjectForBuild(dir, opts); err != nil {
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
		InitConfig:     defaultInitCfg,
		Tag:            tag,
		Boxes:          images,
		BuildDir:       filepath.Join(dir, ".build"),
		Containerfiles: make(map[string]string),
		GlobalOrder:    globalOrder,
		RequestedBoxes: opts.RequestedBoxes,
	}

	// Derive each image's content-stable identity (ai.opencharly.version)
	// from per-entity versions now that the base chain + auto-intermediates are
	// materialized (the build render/version-compute machinery, sdk/deploykit).
	if err := deploykit.ComputeEffectiveVersions(g.Boxes, candyModelMap(g.Candies)); err != nil {
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
		if err := kit.AtomicWriteFile(filepath.Join(g.Dir, name), []byte(b.String()), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	return nil
}

// resolveBuilderStage is the SHARED OpResolve Invoke+decode for the builder BUILDER leg (R3 —
// ONE OpResolve path serving BOTH the `external_builder:`-selected out-of-tree builders and the
// four DETECTION-builders). It marshals the render context (spec.BuilderResolveInput) as op.Params
// and a spec.BuildEnv descriptor as op.Env (so a plugin can tailor per distro/image), Invokes the
// provider's OpResolve, and returns the decoded reply UNVALIDATED — the caller enforces the
// emptiness rule appropriate to its path (external_builder + detection multi-stage require a
// non-empty Stage; the inline cargo path requires a non-empty InlineFragment).
func resolveBuilderStage(prov Provider, word string, in spec.BuilderResolveInput, img *buildkit.ResolvedBox) (spec.BuilderResolveReply, error) {
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
func resolveExternalBuilder(prov Provider, word, candyName string, img *buildkit.ResolvedBox) (spec.BuilderResolveReply, error) {
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
// Called post-main-FROM (right after deploykit EmitExternalBuilderArtifacts) so the COPY lands in
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
		if layer == nil || len(layer.GetBakePlugin()) == 0 {
			continue
		}
		for _, ref := range layer.GetBakePlugin() {
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
			if plugin.GetSourceDir() == "" {
				return fmt.Errorf("candy %q: bake_plugin %q has no source dir to build from", candyName, key)
			}
			binPath, err := buildPluginBinary(context.Background(), plugin.GetSourceDir(), key)
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
			if plugin.IsPluginCandy() && len(plugin.GetPluginProviders()) > 0 {
				providers := plugin.GetPluginProviders()
				lines := make([]string, len(providers))
				for i, c := range providers {
					lines[i] = c // PluginCapability is a "<class>:<word>" string
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

// collectBuilderRuntimeEnv → deploykit.Generator.CollectBuilderRuntimeEnv (P8 shim).
// Used by the host render-prep's buildBakedMetadata (the env_candy + path_append labels).
func (g *Generator) collectBuilderRuntimeEnv(candyOrder []string, img *buildkit.ResolvedBox) []*kit.EnvConfig {
	return g.toDeploykit().CollectBuilderRuntimeEnv(candyOrder, img)
}

// buildStageContext creates the render context passed to a builder plugin's OpResolve leg (via deploykit.BuilderResolveInputFrom).
// buildStageContext → deploykit.Generator.BuildStageContext (P8 shim). Used by the
// host resolveInlineBuilderSeam (the render-seam reverse leg, #67).
func (g *Generator) buildStageContext(layer spec.CandyReader, builderName string, builderDef *BuilderDef, img *buildkit.ResolvedBox, builderRef string) *spec.BuildStageContext {
	return g.toDeploykit().BuildStageContext(layer, builderName, builderDef, img, builderRef)
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
func candyStatus(c spec.CandyReader) string {
	if c == nil {
		return StatusTesting
	}
	return resolveStatus(c.GetStatus())
}

// descriptionInfo moved to sdk/deploykit (deploykit.DescriptionInfo) in K5-Unit-1 —
// shared with the deploy state-model body (MergeDeployOntoMetadata reads it). charly
// call sites (config.go / unified.go / host_build_feature.go / render_baked_metadata.go)
// call deploykit.DescriptionInfo directly.

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
		if layer.GetRemote() {
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
		if !layer.GetRemote() {
			continue
		}
		tmp, err := os.MkdirTemp(candyRoot, "."+layer.GetName()+".tmp.*")
		if err != nil {
			return err
		}
		// Copy the candy's CONTENTS (trailing /.) into the temp so the versioned
		// dir holds the files directly (the Containerfile COPYs `<dir>/ /`).
		cmd := exec.Command("cp", "-a", layer.GetSourceDir()+"/.", tmp)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("copying remote candy %s: %s: %w", ref, string(out), err)
		}
		if err := kit.InstallDirAtomic(tmp, filepath.Join(candyRoot, deploykit.CandyStageDirName(layer))); err != nil {
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
		if l.GetRemote() && l.GetSourceDir() != "" {
			suffix := filepath.Join(l.GetSubPathPrefix(), l.GetName()) // e.g. "candy/pixi"
			if trimmed, ok := strings.CutSuffix(l.GetSourceDir(), suffix); ok {
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

// candyMapKey returns the key under which a candy is stored in g.Candies: the
// fully-qualified remote ref (RepoPath/SubPathPrefix/Name) for remote candies,
// the short name for local ones. Use this whenever code holds a *Candy but
// needs to look it up in g.Candies, since a remote candy's short Name does
// NOT match its map key. (deploykit.Generator.CandyCopySource — the COPY
// source path resolver — is the sdk-side render helper; charly core's own
// wrapper was dead, K3, and is gone.)
// candyMapKey → deploykit.CandyMapKey.

// candyByName resolves a candy by its INTRINSIC bare name against g.Candies.
// It is the FORWARD counterpart of deploykit.CandyMapKey (which maps a *Candy back to its
// store key): a LOCAL candy is keyed bare == Name, so the direct lookup hits; a
// REMOTE candy (e.g. a deploy's add_candy: pulled via ResolveOpts.ExtraCandyRefs)
// is keyed under its fully-qualified ref (deploykit.CandyMapKey), so the direct bare lookup
// MISSES and we fall back to matching the Candy's own Name. Every call site that
// holds a bare candy name (a plan step's CandyName; an overlay-candy name from
// collectOverlayCandies / p.AddCandies) and needs the *Candy goes through here, so
// a remote add_candy overlay layer resolves instead of being silently skipped
// (the add_candy-on-pod-overlay "candy not found" / skipped-stage class).
func (g *Generator) candyByName(name string) spec.CandyReader {
	if g == nil {
		return nil
	}
	if c := g.Candies[name]; c != nil {
		return c
	}
	for _, c := range g.Candies {
		if c != nil && c.GetName() == name {
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
// candyStageDirName → deploykit.CandyStageDirName.
