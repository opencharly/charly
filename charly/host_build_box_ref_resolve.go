package main

// host_build_box_ref_resolve.go — the "box-ref-resolve" HostBuild seam behind the compiled-in
// candy/plugin-build's build:ensure word (core-min wave 3, build-engine cluster relocation).
//
// The former core ensure-image helper's ORCHESTRATION (decide pull-vs-build, exec `podman
// pull`/`podman tag`, remote-build fallback) moved out of charly core into candy/plugin-build's
// new build:ensure word — the file it used to live in is DELETED. What COULD NOT move is the
// project-coupled RESOLUTION a short/full image identifier needs (ResolveBox / FindBoxByLeaf /
// ResolveBoxRef — loader-cone, still core, tracked K1/K3 residue): this file keeps that pure,
// side-effect-free resolve logic (verbatim from the file it used to live in, minus the
// remote-ref branch the plugin now filters BEFORE ever reaching this seam) and exposes it as the
// generic "box-ref-resolve" HostBuild action noun. builder_venue.go's injected ResolveImage
// closure calls resolveImageRefForEnsure directly (same package, same process — no seam hop
// needed there); only the out-of-process plugin reaches this via HostBuild.
//
// #55 step3 3-II CONFIRMED (not moved): build:ensure's box-ref resolve is UNRELATED to that
// cutover's scope (the pod-overlay BUILD envelope's relocation) — its logic stays verbatim,
// tracked K1/K3 loader-cone residue as documented above.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

const boxRefResolveBuilderKind = "box-ref-resolve"

// resolveImageRefForEnsure converts a user-authored image identifier into a fully-qualified
// registry ref usable for `LocalImageExists`. Short names need cfg; full refs pass through.
// (Ported verbatim from the deleted former core ensure-image helper file; the remote-ref branch is gone —
// callers of THIS function, both host-internal (builder_venue.go) and the build:ensure plugin
// via the box-ref-resolve seam, already route a remote `@github.com/...` ref through
// ResolveRemoteImage / the "remote-image-resolve" seam instead of calling this.)
func resolveImageRefForEnsure(image string, cfg *Config, projectDir string) (string, error) {
	if image == "" {
		return "", fmt.Errorf("empty image")
	}
	if kit.LooksLikeFullRef(image) {
		return image, nil
	}
	if cfg == nil {
		return "", fmt.Errorf("short name %q requires a project directory with charly.yml", image)
	}
	vopts, err := resolveVocabOpts(projectDir, loaderkit.ResolveOpts{})
	if err != nil {
		return "", fmt.Errorf("resolving %q via charly.yml: %w", image, err)
	}
	resolved, err := deploykit.ResolveSpecBox(cfg, image, "", projectDir, specResolveOpts(vopts))
	if err != nil {
		return "", fmt.Errorf("resolving %q via charly.yml: %w", image, err)
	}
	return kit.ResolveShellImageRef(resolved.Registry, resolved.Name, ""), nil
}

// buildableShortNameForEnsure returns the short name (project charly.yml key) this identifier
// maps to, or "" when no local build-fallback is possible. Ported verbatim from the deleted
// the former core ensure-image helper's buildableShortName.
//
// Algorithm:
//   - Short names (no slash, no @prefix) are returned as-is when `cfg.Box[name]` exists.
//   - Full registry refs have their basename (last path segment, before the tag) extracted and
//     resolved via FindBoxByLeaf, which searches the root image map AND every imported
//     namespace.
//   - Remote `@github.com/...` refs are skipped (defense-in-depth; the build:ensure plugin
//     already routes a remote ref through the separate remote-image-resolve seam before it
//     would ever reach this function).
func buildableShortNameForEnsure(image string, cfg *Config) string {
	if cfg == nil || cfg.Box == nil || image == "" {
		return ""
	}
	stripped := kit.StripURLScheme(image)
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

// hostBuildBoxRefResolve is the "box-ref-resolve" host-builder: given a user-authored image
// identifier (never a remote @github.com/... ref — the plugin filters those before calling
// this seam), resolves it against the project's charly.yml for both the pull/exists ref and any
// local build-fallback short name.
func hostBuildBoxRefResolve(_ context.Context, req spec.BoxRefResolveRequest, _ buildEngineContext) (spec.BoxRefResolveReply, error) {
	if req.Image == "" {
		return spec.BoxRefResolveReply{}, fmt.Errorf("box-ref-resolve: empty image identifier")
	}
	dir := req.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return spec.BoxRefResolveReply{}, err
		}
		dir = cwd
	}
	cfg, _ := LoadConfig(dir)

	var reply spec.BoxRefResolveReply
	if ref, err := resolveImageRefForEnsure(req.Image, cfg, dir); err == nil {
		reply.ExistsRef = ref
		reply.PullRef = ref
	}

	short := buildableShortNameForEnsure(req.Image, cfg)
	reply.BuildFallbackShort = short
	if short != "" && cfg != nil {
		if vopts, oerr := resolveVocabOpts(dir, loaderkit.ResolveOpts{}); oerr == nil {
			if resolved, err := deploykit.ResolveSpecBox(cfg, short, "", dir, specResolveOpts(vopts)); err == nil && resolved != nil {
				reply.ProducedRef = kit.ResolveShellImageRef(resolved.Registry, resolved.Name, "")
			}
		}
	}
	return reply, nil
}

var _ = func() bool {
	registerHostBuilder(boxRefResolveBuilderKind, typedHostBuilder(boxRefResolveBuilderKind, hostBuildBoxRefResolve))
	return true
}()
