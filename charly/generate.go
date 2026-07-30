package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
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

	// ExtraCandyRefs is the ORIGINAL loaderkit.ResolveOpts.ExtraCandyRefs this Generator was
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

	// (The per-image builder-reply caches externalBuilderReplies/detectionBuilderReplies
	// moved to the deploykit render Generator with the BUILDER-render engine, K3-A; the
	// dkGen memo below keeps them stable across an image's render methods.)

	// dkGen caches the sdk/deploykit render Generator (P8). Built once by
	// toDeploykit() and reused across an image's render so the deploykit-side
	// per-image builder-reply caches persist across the render methods (which are
	// relocating onto deploykit.Generator). Containerfiles is a shared map ref so
	// writes propagate; Candies/Boxes are stable once this Generator's constructor
	// (newCandyScanGenerator today; the deleted NewGenerator formerly) returns.
	dkGen *deploykit.Generator
}

// resolveUserContext detects existing user in base image or uses configured values
func (g *Generator) resolveUserContext(img *spec.ResolvedBox) {
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

// --- verb:oci adopt-user dispatch (folded from the retired oci_plugin.go) ---
//
// The render-seam-floor host Generator's resolveUserContext (above) probes an external base
// image's /etc/passwd for the adopt-user via the compiled-in candy/plugin-oci (verb:oci); the
// go-containerregistry engine + the actual probe live OUT-OF-PROCESS there. Core keeps only this
// thin registry-dispatch, its sole consumer being resolveUserContext (and the render-parity test).
// verb:oci is a pure INTERNAL RPC keyed by an oci_op ENV discriminator (mirroring the vm plugin's
// VmOp) — the request struct rides Params, the leg selector rides Env — NOT a `plugin_input`-
// enveloped check verb. It is COMPILED INTO charly by default (compiled_plugins:), so
// providerRegistry resolves it in-process and project-lessly; connectPluginByWord covers the
// baked / project-source coexist paths (the registry-first pattern credential_plugin.go uses).

// ociOpInspectUser is the env-JSON selector matching candy/plugin-oci's ociEnv{OciOp}.
const ociOpInspectUser = "inspect-user"

// ociProvider resolves verb:oci. Registry-first so a COMPILED-IN plugin resolves in-process and
// project-lessly; falls back to connectPluginByWord for the baked / project-source coexist paths.
func ociProvider() (Provider, bool) {
	if p, ok := providerRegistry.resolve(ClassVerb, "oci"); ok {
		return p, true
	}
	return connectPluginByWord(ClassVerb, "oci")
}

// invokeOciInspectUser probes a remote image's /etc/passwd for the user at uid via verb:oci,
// returning the spec.UserInfo (Found=false when no such user / the image can't be inspected).
func invokeOciInspectUser(ref string, uid int) (spec.UserInfo, error) {
	prov, ok := ociProvider()
	if !ok {
		return spec.UserInfo{}, fmt.Errorf(
			"oci plugin (verb:oci) did not connect — candy/plugin-oci is compiled into charly " +
				"(compiled_plugins) by default; on a custom build install it alongside charly " +
				"(/usr/lib/charly/plugins) or run from a project composing it")
	}
	paramsJSON, err := marshalJSON(spec.ImageUserInput{Ref: ref, UID: uid})
	if err != nil {
		return spec.UserInfo{}, err
	}
	envJSON, err := marshalJSON(map[string]string{"oci_op": ociOpInspectUser})
	if err != nil {
		return spec.UserInfo{}, err
	}
	out, err := prov.Invoke(context.Background(), &Operation{
		Reserved: "oci",
		Op:       OpRun,
		Params:   paramsJSON,
		Env:      envJSON,
	})
	if err != nil {
		return spec.UserInfo{}, err
	}
	if out == nil {
		return spec.UserInfo{}, fmt.Errorf("oci: verb:oci returned no result")
	}
	var info spec.UserInfo
	if err := json.Unmarshal(out.JSON, &info); err != nil {
		return spec.UserInfo{}, fmt.Errorf("oci inspect-user: decode reply: %w", err)
	}
	return info, nil
}

// newCandyScanGenerator builds a Generator populated with Config+Candies+Boxes+Dir+BuildDir — a
// candy scan + a PLAIN per-box buildkit.ResolveBox pass (NO ComputeIntermediates / GlobalCandyOrder
// / ComputeEffectiveVersions / RenderPrepAll) — the minimal state the render-seam floor's 2
// remaining reverse-channel consumers need: ensureBuildersConnected touches only Config/Dir;
// resolveInlineBuilderSeam's resolveBuilderStage reads img.Tags/img.Name off gen.Boxes[boxName]
// (verified by reading resolveBuilderStage's own body — NOT img.Base/BootstrapBuilderImage, so
// skipping ComputeIntermediates' auto-intermediate Base-rewrite is safe here). MUCH cheaper than
// the deleted NewGenerator (#55 step3 3-II): the build-engine RESOLVE's expensive parts (the candy
// SCAN's network-bound remote fetches, ComputeIntermediates, GlobalCandyOrder,
// ComputeEffectiveVersions, RenderPrepAll) now run entirely plugin-side (candy/plugin-build's
// resolveBuildEngine, K3) — recomputing THOSE again host-side for the render-seam cache was 100%
// wasted work (proven dead by call-graph: nothing downstream ever read the second Generator's
// render-prep output); ResolveBox itself is pure, in-memory, and genuinely still needed (RCA'd: a
// first cut that dropped it entirely broke resolveInlineBuilderSeam's img.Tags/img.Name — caught
// before merge by re-tracing every reader of gen.Boxes[boxName], not by the box-generate smoke
// test, which never exercises the inline-builder path). Skips the build-time plugin connect +
// pre-build validate the deleted NewGenerator also used to run: both already ran plugin-side
// (resolveBuildEngine steps 4-5) by the time this is reached through the normal build/generate
// path, and ensureBuildersConnected connects on demand itself when reached any other way (e.g. the
// loadRenderGen defensive fallback).
func newCandyScanGenerator(dir string, includeDisabled bool, extraCandyRefs []string) (*Generator, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	defaultDistroCfg, _, defaultInitCfg, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		return nil, fmt.Errorf("loading default build config: %w", err)
	}
	RegisterBuildVocabulary(defaultDistroCfg)
	opts := loaderkit.ResolveOpts{IncludeDisabled: includeDisabled, ExtraCandyRefs: extraCandyRefs, InitCfg: defaultInitCfg}
	layers, err := ScanAllCandyWithConfigOpts(dir, cfg, opts)
	if err != nil {
		return nil, err
	}
	vopts, err := resolveVocabOpts(dir, opts)
	if err != nil {
		return nil, err
	}
	// #55 Cluster-B: the PURE per-box resolve runs inside the deploykit box-resolve bridge
	// (kit→kit), returning wire-clean *spec.ResolvedBox — the render-seam floor's 2 consumers
	// (resolveInlineBuilderSeam/ensureBuildersConnected) read only Name/Tags off these. NOT the
	// resolved-project envelope InvokeProvider path (would recurse an in-flight build:generate).
	images, err := deploykit.ResolveAllSpecBoxes(cfg, ComputeCalVer(), dir, specResolveOpts(vopts))
	if err != nil {
		return nil, err
	}
	return &Generator{
		Dir:            dir,
		Config:         cfg,
		Candies:        layers,
		InitConfig:     defaultInitCfg,
		Boxes:          images,
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

// resolveExternalBuilder Invokes an `external_builder:`-selected out-of-tree builder provider's
// OpResolve and returns the decoded BuilderResolveReply — the BUILDER-leg analogue of
// invokeVerbBuildEmit. It sends a MINIMAL render context (the requesting candy name only — an
// out-of-tree builder renders a self-contained stage that reads none of the detection fields),
// then requires a non-empty Stage (a mis-selected word producing no build-context builder fails
// LOUDLY). Shares the OpResolve Invoke with the detection path via resolveBuilderStage (R3).
func resolveExternalBuilder(prov Provider, word, candyName string, img *spec.ResolvedBox) (spec.BuilderResolveReply, error) {
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
		if err := kit.InstallDirAtomic(tmp, filepath.Join(candyRoot, deploykit.CandyStageDirName(layer))); err != nil {
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
// REMOTE candy (e.g. a deploy's add_candy: pulled via loaderkit.ResolveOpts.ExtraCandyRefs)
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
