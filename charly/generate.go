package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/opencharly/spec/lock"
	"github.com/opencharly/spec/spec"
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
	InitConfig     *spec.InitConfig
	Tag            string
	Boxes          map[string]*spec.ResolvedBox
	BuildDir       string
	Containerfiles map[string]string // cached content per image (used by charly build to pipe via stdin)
	GlobalOrder    []string          // popularity-weighted global candy order for cache optimization

	// RequestedBoxes scopes which Containerfiles Generate() writes: when
	// non-empty, only the named boxes and their transitive deps (Base + format
	// builders + bootstrap builder) are emitted — the SAME filterBox set the
	// build path uses to scope `podman build` (R3, build/generate unified). Empty
	// means "every enabled box" (the bare `charly box generate` / `generate all`
	// and full `charly box build` behaviour). The whole resolved graph
	// (intermediates, global candy order, effective versions) is computed by
	// whichever constructor built this Generator (candy/plugin-build's
	// resolveBuildEngine for a real build/generate drive; #55 step3 3-II deleted
	// the former host-side NewGenerator that used to do this) — only the
	// per-box emission loop is scoped by this field.
	RequestedBoxes []string

	// ExtraCandyRefs is the ORIGINAL spec.ResolveOpts.ExtraCandyRefs this Generator was
	// constructed with (a pod-overlay deploy's add_candy: refs, possibly REMOTE/
	// qualified — e.g. "@github.com/…:vTAG"). Candies (bare-keyed, post-scan) cannot
	// stand in for this: a bare candy NAME re-passed as an ExtraCandyRefs entry is a
	// silent no-op for a remote candy (ScanAllCandyWithConfigOpts's addRef gates on
	// IsRemoteCandyRef), so a SECOND, INDEPENDENT resolved-project re-fetch (e.g.
	// candy/plugin-installstep's own getGenerator, reached via dispatchOCIStep's
	// BuildEnv.ExtraCandyRefs) needs the ORIGINAL qualified refs, not a re-derivation
	// from this Generator's own scan result. RCA'd K1-alpha regression: an overlay
	// build's OpStep emit ("task emit: candy %q not found") for a remote add_candy
	// candy — see oci_step_emit.go's dispatchOCIStep.
	ExtraCandyRefs []string

	// DevLocalPkg, when true, makes localpkg candies (the charly toolchain) build
	// from LOCAL in-development source instead of downloading the published
	// release. Set ONLY for disposable check-bed image builds (the check-bed runner
	// passes `--dev-local-pkg`), so a bed always tests the in-development charly;
	// a production box build leaves it false. See deploykit.RenderLocalPkgImageInstall.
	DevLocalPkg bool
}

// newCandyScanGenerator builds a Generator populated with Config+Candies+Boxes+Dir+BuildDir — a
// candy scan + the caller-supplied resolved-box set (NO ComputeIntermediates / GlobalCandyOrder
// / ComputeEffectiveVersions / RenderPrepAll, and NO host-side box resolve) — the minimal state
// the render-seam floor's 2 remaining reverse-channel consumers need: ensureBuildersConnected
// touches only Config/Dir; resolveInlineBuilderSeam's resolveBuilderStage reads img.Tags/img.Name
// off gen.Boxes[boxName] (verified by reading resolveBuilderStage's own body — NOT
// img.Base/BootstrapBuilderImage, so skipping ComputeIntermediates' auto-intermediate Base-rewrite
// is safe here). MUCH cheaper than the deleted NewGenerator (#55 step3 3-II): the build-engine
// RESOLVE's expensive parts (the candy SCAN's network-bound remote fetches, the box RESOLVE,
// ComputeIntermediates, GlobalCandyOrder, ComputeEffectiveVersions, RenderPrepAll) now run
// entirely plugin-side (candy/plugin-build's resolveBuildEngine, K3) — recomputing THOSE again
// host-side for the render-seam cache was 100% wasted work (proven dead by call-graph: nothing
// downstream ever read the second Generator's render-prep output). The boxes themselves are now
// PUSHED by the plugin (#55 coneB2 Class B): candy/plugin-build's resolveBuildEngine resolves them
// via buildkit.ResolveAllBox + deploykit.SpecBoxes and ships them on #ResolvedProjectRequest.boxes;
// hostBuildPrep passes them through here. RCA'd: a first cut that dropped the boxes entirely broke
// resolveInlineBuilderSeam's img.Tags/img.Name — caught before merge by re-tracing every reader of
// gen.Boxes[boxName], not by the box-generate smoke test, which never exercises the inline-builder
// path. Skips the build-time plugin connect + pre-build validate the deleted NewGenerator also used
// to run: both already ran plugin-side (resolveBuildEngine steps 4-5) by the time this is reached
// through the normal build/generate path, and ensureBuildersConnected connects on demand itself when
// reached any other way (e.g. the loadRenderGen defensive fallback, which passes a nil boxes map —
// provably unreachable in production).
func newCandyScanGenerator(dir string, includeDisabled bool, extraCandyRefs []string, boxes map[string]*spec.ResolvedBox) (*Generator, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	defaultDistroCfg, _, defaultInitCfg, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		return nil, fmt.Errorf("loading default build config: %w", err)
	}
	RegisterBuildVocabulary(defaultDistroCfg)
	opts := spec.ResolveOpts{IncludeDisabled: includeDisabled, ExtraCandyRefs: extraCandyRefs, InitCfg: defaultInitCfg}
	layers, err := ScanAllCandyWithConfigOpts(dir, cfg, opts)
	if err != nil {
		return nil, err
	}
	// #55 coneB2 Class B: the box RESOLVE no longer runs host-side (no deploykit import). The
	// render-seam floor's 2 consumers (resolveInlineBuilderSeam/ensureBuildersConnected) read only
	// Name/Tags off the PUSHED boxes — supplied by the plugin (buildkit.ResolveAllBox +
	// deploykit.SpecBoxes over #ResolvedProjectRequest.boxes), NOT recomputed here. NOT the
	// resolved-project envelope InvokeProvider path (would recurse an in-flight build:generate).
	return &Generator{
		Dir:            dir,
		Config:         cfg,
		Candies:        layers,
		InitConfig:     defaultInitCfg,
		Boxes:          boxes,
		BuildDir:       filepath.Join(dir, ".build"),
		Containerfiles: make(map[string]string),
		ExtraCandyRefs: extraCandyRefs,
	}, nil
}

// baselineContextIgnore reads the context_ignore_baseline list from the embedded charly.yml via
// the shared minimal decoder; panics if the embed is malformed or the directive is empty (a
// build-time invariant, never a runtime input). Read ONLY by hostBuildContextIgnoreBaseline
// (host_build_buildengine.go), the thin data leg candy/plugin-build's writeContextIgnore fetches
// this bootstrap-embedded fact through (K3 host-prep move — cleanStaleBuildDirs/writeContextIgnore/
// createRemoteCandyCopies/ensureCharlyBinaryFresh themselves moved to candy/plugin-build/host_prep.go;
// this ONE piece of static data stays host because a separate Go module cannot //go:embed
// charly/charly.yml).
var baselineContextIgnore = parseEmbeddedContextIgnoreBaseline()

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

// resolveBuilderStage is the SHARED OpResolve Invoke+decode for the builder BUILDER leg (R3 —
// ONE OpResolve path serving BOTH the `external_builder:`-selected out-of-tree builders and the
// four DETECTION-builders). It marshals the render context (spec.BuilderResolveInput) as op.Params
// and a spec.BuildEnv descriptor as op.Env (so a plugin can tailor per distro/image), Invokes the
// provider's OpResolve, and returns the decoded reply UNVALIDATED — the caller enforces the
// emptiness rule appropriate to its path (external_builder + detection multi-stage require a
// non-empty Stage; the inline cargo path requires a non-empty InlineFragment).
func resolveBuilderStage(prov Provider, word string, in spec.BuilderResolveInput, img *spec.ResolvedBox) (spec.BuilderResolveReply, error) {
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

// emitBakedPlugins moved to sdk/deploykit (deploykit.EmitBakedPlugins, K3 build-tail move,
// coneB-buildtail): buildPluginBinary is 100% pure os/exec (proven by the already-moved
// ensureCharlyBinaryFresh) — no host-only dependency — so the former "bake-plugins" HostBuild
// round-trip (charly/host_build_bake_plugins.go, DELETED) is unnecessary; NewRenderGeneratorFromProject
// wires deploykit.EmitBakedPlugins directly.

// descriptionInfo moved to sdk/deploykit (deploykit.DescriptionInfo) in K5-Unit-1 —
// shared with the deploy state-model body (MergeDeployOntoMetadata reads it). charly
// call sites (config.go / unified.go / host_build_feature.go / render_baked_metadata.go)
// call deploykit.DescriptionInfo directly. The candy/box maturity-rung helpers
// (resolveStatus/candyStatus/statusSeverity/worstStatus/Status*) moved to
// sdk/buildkit (buildkit.ResolveStatus/CandyStatus/StatusSeverity/WorstStatus/Status*)
// in the BUILD-cone cutover — pure over spec.CandyReader, no loader coupling.

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
		if err := lock.InstallDirAtomic(tmp, filepath.Join(candyRoot, spec.CandyStageDirName(layer))); err != nil {
			return fmt.Errorf("installing remote candy %s: %w", ref, err)
		}
	}

	return nil
}

// remoteBuildConfigCacheRoot/materializeBuildConfigAsset/rewriteHeaderCopyForRemote deleted
// (coneB-buildtail): dead in this Generator's only surviving path — their sole caller,
// EmitInitFragmentStages, runs in deploykit's Generate() per-box render loop, never
// RenderPrepBox (confirmed by call-graph trace, same finding as ValidateEgress/EmitBakedPlugins).
// sdk/deploykit/header_copy_remote.go already carries the pure, plugin-side reproduction
// NewRenderGeneratorFromProject wires directly — no host round-trip needed.

// candyMapKey returns the key under which a candy is stored in g.Candies: the
// fully-qualified remote ref (RepoPath/SubPathPrefix/Name) for remote candies,
// the short name for local ones. Use this whenever code holds a spec.CandyReader but
// needs to look it up in g.Candies, since a remote candy's short Name does
// NOT match its map key. (deploykit.Generator.CandyCopySource — the COPY
// source path resolver — is the sdk-side render helper; charly core's own
// wrapper was dead, K3, and is gone.)
// candyMapKey → deploykit.CandyMapKey.

// candyByName resolves a candy by its INTRINSIC bare name against g.Candies.
// It is the FORWARD counterpart of deploykit.CandyMapKey (which maps a candy back to its
// store key): a LOCAL candy is keyed bare == Name, so the direct lookup hits; a
// REMOTE candy (e.g. a deploy's add_candy: pulled via spec.ResolveOpts.ExtraCandyRefs)
// is keyed under its fully-qualified ref (deploykit.CandyMapKey), so the direct bare lookup
// MISSES and we fall back to matching the candy's own Name. Every call site that
// holds a bare candy name (a plan step's CandyName; an overlay-candy name from
// collectOverlayCandies / p.AddCandies) and needs the candy goes through here, so
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
