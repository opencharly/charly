package build

// ensure.go — the candy-side DRIVE behind the build:ensure word (core-min wave 3, build-engine
// cluster relocation): ensure a container image is present in local podman storage, falling back
// to a local (or remote-cached) build when the identifier maps to a project charly.yml entry.
//
// This is the SAME contract the former core ensure-image helper used to offer (that file is
// DELETED) plus the former core cross-engine-transfer tier — every former Go-level caller now
// reaches it through dispatchBuildEnsure (charly/dispatch_build_ensure.go), which Invokes this
// word.
//
// Three tiers, each independent, mirroring the deleted core transfer + ensure-image helpers
// exactly:
//
//  1. Already-present short-circuit (kit.LocalImageExists in the run engine).
//  2. Cross-engine transfer (`docker save | podman load`) when the build engine != the run
//     engine AND the image is present in the build engine's storage.
//  3. Resolve + pull + build-fallback: resolve the identifier against the project's charly.yml
//     PLUGIN-SIDE via the K1 loader reverse legs (loaderkit.LoadUnified — a remote
//     @github.com/... ref instead routes through "remote-image-resolve" for the git clone/cache,
//     then resolveRemoteImageRef), attempt `podman pull`, and on failure fall back to a local
//     build — reached via the SAME in-process runBoxBuild this candy already owns for the
//     build:box word (no new build seam), then tag-aliasing the built image onto the requested
//     ref via aliasBuiltImageOntoRequestedRef, whose doc comment carries the cold-store ordering
//     constraint that alias must respect.
//
// Tier 3 needs NO host round trip anymore: the box-ref resolve (the exists/pull ref +
// the buildable short name) is computed plugin-side off the cfg the K1 loader reverse legs
// return (byte-identical Registry/Name to the former host-side deploykit.ResolveSpecBox path —
// buildkit config_resolve.go:101-103: resolved.Registry = img.Registry || cfg.Defaults.Registry,
// resolved.Name = leaf after namespace descent). The "box-ref-resolve" HostBuild seam is DELETED
// (#55 coneK1 #8 — deploykit.ResolveSpecBox shed from the build:ensure path; the host file keeps
// only resolveImageRefForEnsure for the builder-venue reverse leg, coneD territory). Tiers 1-2
// and the podman pull/tag exec are pure sdk/kit + os/exec.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// runBoxEnsure is the build:ensure Invoke body.
func runBoxEnsure(ctx context.Context, ex *sdk.Executor, req spec.BuildEnsureRequest) error {
	if req.Image == "" {
		return fmt.Errorf("ensure-image: empty image identifier")
	}
	runEngine := req.RunEngine
	if runEngine == "" {
		runEngine = "podman"
	}

	// Tier 1: already present. Only matches a FULL ref (podman/docker "image exists" needs a
	// real storage key — a bare short name never matches here); a short name's existence check
	// happens in tier 3 via the plugin-side resolved ref. Prints the SAME "present" line the
	// former core ensure-image helper's top-level short-circuit did, so every caller (charly box
	// pull included) sees consistent progress output regardless of which tier hit.
	if kit.LocalImageExists(runEngine, req.Image) {
		fmt.Fprintf(os.Stderr, "ensure-image: %s present\n", req.Image)
		return nil
	}

	// Tier 2: cross-engine transfer — faster than a network pull and works offline.
	if req.BuildEngine != "" && req.BuildEngine != runEngine && kit.LocalImageExists(req.BuildEngine, req.Image) {
		return kit.TransferImage(req.BuildEngine, runEngine, req.Image)
	}

	// Tier 3: resolve + pull + build-fallback.
	return ensureViaResolveAndBuild(ctx, ex, req.Image, req.Dir)
}

// ensureViaResolveAndBuild implements tier 3. A remote (@github.com/...) ref resolves via the
// "remote-image-resolve" seam (host-side clone/cache ONLY; the box-RESOLVE + registry pull ref
// run plugin-side — see resolveRemoteImageRef); every other identifier resolves PLUGIN-SIDE
// (ensureProjectRef loads the cfg via the K1 loader reverse legs + resolves Registry/Name
// directly, no host seam). Both branches mirror the deleted core ensure-image helper's algorithm
// verbatim: check existence of the resolved ref, attempt a pull, and on failure fall back to a
// build.
func ensureViaResolveAndBuild(ctx context.Context, ex *sdk.Executor, image, dir string) error {
	stripped := kit.StripURLScheme(image)
	if spec.IsRemoteImageRef(stripped) {
		return ensureRemoteRef(ctx, ex, image, stripped)
	}
	return ensureProjectRef(ctx, ex, image, dir)
}

func ensureRemoteRef(ctx context.Context, ex *sdk.Executor, image, stripped string) error {
	rr, rerr := remoteImageResolve(ctx, ex, stripped, "")
	if rerr != nil || rr.Error != "" {
		errMsg := rr.Error
		if rerr != nil {
			errMsg = rerr.Error()
		}
		fmt.Fprintf(os.Stderr, "ensure-image: resolve %s: %s\n", image, errMsg)
		return fmt.Errorf("ensure-image %q: pull failed and remote build failed: %s", image, errMsg)
	}
	// Compute the registry pull ref PLUGIN-SIDE: the "remote-image-resolve" host seam now does
	// ONLY the git clone/cache (K1 loader wave — sheds deploykit.ResolveSpecBox from charly
	// core); the plugin loads the cached repo's cfg via the K1 loader reverse legs + reads the
	// box's registry/name itself (byte-identical to the former host-side deploykit.ResolveSpecBox
	// → ResolveShellImageRef path: img.Registry || cfg.Defaults.Registry, buildkit
	// config_resolve.go:101-103). An empty ref (box not found / no registry configured) skips the
	// pull attempt and falls straight to the build fallback — preserving the former behavior
	// where a remote box with no resolvable pull ref just built from source.
	imageRef := resolveRemoteImageRef(ctx, ex, rr.CacheDir, rr.BoxName)
	if imageRef != "" {
		if kit.LocalImageExists("podman", imageRef) {
			fmt.Fprintf(os.Stderr, "ensure-image: %s present\n", imageRef)
			return nil
		}
		fmt.Fprintf(os.Stderr, "ensure-image: pulling %s\n", imageRef)
		if err := podmanPull(ctx, imageRef); err == nil {
			return nil
		} else {
			fmt.Fprintf(os.Stderr, "ensure-image: pull %s failed: %v\n", imageRef, err)
		}
	}
	fmt.Fprintf(os.Stderr, "ensure-image: building remote %s from cached source\n", image)
	if _, berr := runBoxBuild(ctx, ex, spec.BuildRequest{Boxes: []string{rr.BoxName}, Dir: rr.CacheDir}); berr != nil {
		return fmt.Errorf("ensure-image %q: pull failed and remote build failed: %w", image, berr)
	}
	return aliasBuiltImageOntoRequestedRef(ctx, image, func() string {
		return resolveRemoteImageRef(ctx, ex, rr.CacheDir, rr.BoxName)
	})
}

// aliasBuiltImageOntoRequestedRef enforces the post-condition every build fallback owes its
// caller: the caller asked for `image` and will run it with `--pull=never`, so after a successful
// build `image` ITSELF must resolve in local storage. A box build produces the project's
// CalVer-tagged ref, so a full input ref (pinned to a different tag, or tagless) needs an alias.
//
// resolveProduced is a CALLBACK, deliberately, because it must run AFTER the build: every producer
// of it bottoms out in ResolveShellImageRef with an empty tag, which READS THE LOCAL IMAGE STORE
// (spec/container/local_image_coneb.go — empty tag → ResolveNewestLocalCalVer). Resolved BEFORE
// the build, on a store that does not yet hold the image, it degrades to the bare
// `<registry>/<name>` — which both EQUALS `image` (silently defeating a `produced != image` guard)
// and names a ref the build never created. That was a real cold-store defect: the first run on a
// fresh store left the requested ref absent and failed ~1000 log lines later with podman's
// `image not known`, while every subsequent run passed because run 1's build had seeded the store
// — so the bug reproduced only on the fresh-machine path R10 exists to cover.
//
// A failed alias is a HARD error, not a warning: the caller cannot proceed without the ref, and a
// warning here only relocates the failure to a downstream message that names neither this build
// nor the alias.
func aliasBuiltImageOntoRequestedRef(ctx context.Context, image string, resolveProduced func() string) error {
	// A short-name input carries no tag to satisfy, and a build that already produced the exact
	// requested ref needs nothing further.
	if !kit.LooksLikeFullRef(image) || kit.LocalImageExists("podman", image) {
		return nil
	}
	produced := resolveProduced()
	if produced == "" || produced == image {
		return fmt.Errorf("ensure-image %q: the build reported success but produced no local ref that satisfies it "+
			"(resolved produced ref: %q) — the requested ref is still absent from local storage", image, produced)
	}
	if terr := podmanTag(ctx, produced, image); terr != nil {
		return fmt.Errorf("ensure-image %q: tagging the built %s as the requested ref failed: %w", image, produced, terr)
	}
	fmt.Fprintf(os.Stderr, "ensure-image: tagged %s as %s\n", produced, image)
	return nil
}

// resolveRemoteImageRef computes the registry pull ref for a remote box PLUGIN-SIDE, loading the
// cached repo's cfg via the K1 loader reverse legs (the host "remote-image-resolve" seam now does
// ONLY the git clone/cache — K1 loader wave, deploykit.ResolveSpecBox shed from charly core). It
// returns "" if the box can't be loaded/found, so the caller skips the pull and falls back to a
// build. Byte-identical Registry/Name to the former host-side deploykit.ResolveSpecBox path:
// resolved.Registry = img.Registry || cfg.Defaults.Registry (buildkit/config_resolve.go:101-103),
// then kit.ResolveShellImageRef (the re-export of container.ResolveShellImageRef the former host
// path used). The tag is "" — the caller (ensureRemoteRef) passes an empty tag to the seam, so the
// former host ImageRef was likewise tag-less.
func resolveRemoteImageRef(ctx context.Context, ex *sdk.Executor, dir, boxName string) string {
	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, ex, dir)
	if err != nil || !ok || uf == nil {
		return ""
	}
	cfg := uf.ProjectConfig()
	img, ok := cfg.BoxConfig(boxName)
	if !ok {
		return ""
	}
	registry := img.Registry
	if registry == "" {
		registry = cfg.Defaults.Registry
	}
	return kit.ResolveShellImageRef(registry, boxName, "")
}

func ensureProjectRef(ctx context.Context, ex *sdk.Executor, image, dir string) error {
	// Plugin-side self-resolve (#55 coneK1 #8): load the project cfg via the K1 loader reverse
	// legs + compute all 4 box-ref reply fields directly — NO "box-ref-resolve" HostBuild seam
	// (deleted) and NO deploykit.ResolveSpecBox (the host-side deploykit call is shed from this
	// path). Byte-identical Registry/Name to the former host path: resolved.Registry =
	// img.Registry || cfg.Defaults.Registry, resolved.Name = leaf after namespace descent
	// (buildkit/config_resolve.go:101-103), then kit.ResolveShellImageRef. A nil cfg (load
	// failed / no charly.yml) mirrors the former host behavior where LoadConfig erroring left
	// cfg nil → resolveImageRefForEnsure errored → ExistsRef/PullRef empty + build-fallback
	// attempted against a nil cfg (→ "" → the "no buildable short-name match" error).
	cfg := loadProjectCfgForEnsure(ctx, ex, dir)

	// existsRef doubles as the pull ref — one resolve, one meaning. There is deliberately NO
	// pre-build "produced ref": that resolve reads the local image store and is only meaningful
	// after the build (see aliasBuiltImageOntoRequestedRef).
	existsRef := resolveImageRefPlugin(image, cfg)
	fallbackShort := buildableShortNamePlugin(image, cfg)

	if existsRef != "" && kit.LocalImageExists("podman", existsRef) {
		fmt.Fprintf(os.Stderr, "ensure-image: %s present\n", existsRef)
		return nil
	}

	if existsRef != "" {
		fmt.Fprintf(os.Stderr, "ensure-image: pulling %s\n", existsRef)
		if perr := podmanPull(ctx, existsRef); perr == nil {
			return nil
		} else {
			fmt.Fprintf(os.Stderr, "ensure-image: pull %s failed: %v\n", existsRef, perr)
		}
	}

	if fallbackShort == "" {
		return fmt.Errorf("ensure-image %q: not present locally, pull failed, and no buildable short-name match in charly.yml — make the registry public, log in to the registry, or pre-build the image manually", image)
	}

	fmt.Fprintf(os.Stderr, "ensure-image: building %s locally\n", fallbackShort)
	if _, berr := runBoxBuild(ctx, ex, spec.BuildRequest{
		Boxes:           []string{fallbackShort},
		Dir:             dir,
		IncludeDisabled: true,
		Jobs:            4,
	}); berr != nil {
		return fmt.Errorf("ensure-image %q: pull failed and local build failed: %w", image, berr)
	}

	return aliasBuiltImageOntoRequestedRef(ctx, image, func() string {
		return resolveImageRefPlugin(fallbackShort, cfg)
	})
}

// loadProjectCfgForEnsure loads the project cfg for the build:ensure plugin-side resolve, via the
// K1 loader reverse legs (the SAME load path resolveRemoteImageRef uses). Returns nil on any
// load failure / absent charly.yml so the callers fall through to the build-fallback / "no
// buildable short-name match" error exactly as the former host path did when LoadConfig erroring
// left cfg nil.
func loadProjectCfgForEnsure(ctx context.Context, ex *sdk.Executor, dir string) *spec.Config {
	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, ex, dir)
	if err != nil || !ok || uf == nil {
		return nil
	}
	return uf.ProjectConfig()
}

// resolveImageRefPlugin converts a user-authored image identifier into a fully-qualified
// registry ref usable for `LocalImageExists` / `podman pull`, PLUGIN-SIDE. Full refs pass
// through; short / qualified names resolve against cfg via the namespace-aware ResolveBoxRef
// (byte-identical to the former host resolveImageRefForEnsure → deploykit.ResolveSpecBox →
// buildkit.ResolveBox path: namespace descent via SplitNamespaceRef, resolved.Name = leaf,
// resolved.Registry = img.Registry || nsCfg.Defaults.Registry, then ResolveShellImageRef).
// Returns "" when the identifier can't be resolved (nil cfg, box not found) so the caller skips
// the pull and falls back to a build — mirroring the former host path where a resolve error left
// ExistsRef/PullRef empty.
func resolveImageRefPlugin(image string, cfg *spec.Config) string {
	if image == "" {
		return ""
	}
	if kit.LooksLikeFullRef(image) {
		return image
	}
	if cfg == nil {
		return ""
	}
	img, nsCfg, ok := cfg.ResolveBoxRef(image)
	if !ok {
		return ""
	}
	registry := img.Registry
	if registry == "" {
		registry = nsCfg.Defaults.Registry
	}
	return kit.ResolveShellImageRef(registry, spec.LeafName(image), "")
}

// buildableShortNamePlugin returns the short name (project charly.yml key) this identifier maps
// to, or "" when no local build-fallback is possible. Ported verbatim from the former host
// buildableShortNameForEnsure (charly/host_build_box_ref_resolve.go, deleted in this cutover):
// the host builder is gone, the plugin owns the build-fallback resolve now.
//
// Algorithm:
//   - Short names (no slash, no @prefix) are returned as-is when `cfg.Box[name]` exists.
//   - Full registry refs have their basename (last path segment, before the tag) extracted and
//     resolved via FindBoxByLeaf, which searches the root image map AND every imported
//     namespace.
//   - Remote `@github.com/...` refs are skipped (defense-in-depth; ensureViaResolveAndBuild
//     routes a remote ref through ensureRemoteRef before it would ever reach this function).
func buildableShortNamePlugin(image string, cfg *spec.Config) string {
	if cfg == nil || cfg.Box == nil || image == "" {
		return ""
	}
	stripped := spec.StripURLScheme(image)
	if spec.IsRemoteImageRef(stripped) {
		return ""
	}
	// Strip tag if present. Be careful: a registry like "localhost:5000/foo" has a colon
	// BEFORE the first slash that's the port, not the tag separator.
	work := image
	firstSlash := strings.Index(work, "/")
	lastColon := strings.LastIndex(work, ":")
	if lastColon >= 0 && (firstSlash < 0 || lastColon > firstSlash) {
		work = work[:lastColon]
	}
	// Take the last path segment.
	if i := strings.LastIndex(work, "/"); i >= 0 {
		work = work[i+1:]
	}
	if work == "" {
		return ""
	}
	// A QUALIFIED (namespaced) input resolves directly — `fedora.fedora-builder` names a
	// buildable image as-is; the leaf lookup below can never match a dotted ref.
	if strings.Contains(work, ".") {
		if _, _, ok := cfg.ResolveBoxRef(work); ok {
			return work
		}
	}
	if q, ok := cfg.FindBoxByLeaf(work); ok {
		return q
	}
	return ""
}

// remoteImageResolve calls the "remote-image-resolve" HostBuild seam.
func remoteImageResolve(ctx context.Context, ex *sdk.Executor, ref, tag string) (spec.RemoteImageResolveReply, error) {
	reqJSON, err := json.Marshal(spec.RemoteImageResolveRequest{Ref: ref, Tag: tag})
	if err != nil {
		return spec.RemoteImageResolveReply{}, err
	}
	replyJSON, err := ex.HostBuild(ctx, "remote-image-resolve", reqJSON)
	if err != nil {
		return spec.RemoteImageResolveReply{}, err
	}
	var reply spec.RemoteImageResolveReply
	if err := json.Unmarshal(replyJSON, &reply); err != nil {
		return spec.RemoteImageResolveReply{}, fmt.Errorf("decode remote-image-resolve reply: %w", err)
	}
	return reply, nil
}

// podmanPull invokes `podman pull <ref>` on the local machine. Errors propagate verbatim; the
// caller decides whether to fall back to a local build.
func podmanPull(ctx context.Context, ref string) error {
	cmd := exec.CommandContext(ctx, "podman", "pull", ref)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// podmanTag adds a second tag to an existing local image. Used to satisfy a tag-pinned input ref
// after a local build produced a different (CalVer) tag of the same image.
func podmanTag(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "podman", "tag", src, dst)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
