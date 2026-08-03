package main

// host_build_box_ref_resolve.go — the host-only box-ref resolve helper retained for the
// builder-venue reverse leg (#55 coneK1 #8: the "box-ref-resolve" HostBuild seam itself is
// DELETED — the build:ensure word resolves the box ref PLUGIN-SIDE via the K1 loader reverse
// legs, shedding deploykit.ResolveSpecBox from that path; see candy/plugin-build/ensure.go's
// resolveImageRefPlugin / buildableShortNamePlugin).
//
// What STAYS here is resolveImageRefForEnsure: the project-coupled RESOLUTION a short/full image
// identifier needs, kept for the ONE host-internal caller that cannot cross a process boundary:
// plugin_executor_reverse.go's injected ResolveImage closure (the BuilderStep host-step dispatch,
// plugin_executor_reverse.go:161), which closes over the in-process *Config the deploy walk
// already holds. The former buildableShortNameForEnsure + hostBuildBoxRefResolve host-builder are
// DELETED — the plugin owns the build-fallback resolve now (buildableShortNamePlugin).
//
// #55 coneB-br2: this file SHEDS its deploykit import via the #8 cfg.ResolveBoxRef pattern (the
// SAME byte-identical shed coneK1 #8 BANKED for candy/plugin-build/ensure.go's resolveImageRefPlugin
// — reads ONLY Registry+Name, NOT .Distro; cfg.ResolveBoxRef is pure config-nav, no build
// vocabulary fill needed). The function STAYS host-side (its sole prod caller,
// plugin_executor_reverse.go's BuilderStep ResolveImage closure, is host-internal — coneD's
// separate territory to move the CALLER plugin-side one day); the FILE's deploykit import is gone
// NOW, regardless of that caller move. This REFUTES the prior "this file KEEPS its deploykit
// import; the count drop requires coneD to move the caller" framing — the #8 pattern sheds the
// import while the function stays host-side.

import (
	"fmt"

	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/spec"
)

// resolveImageRefForEnsure converts a user-authored image identifier into a fully-qualified
// registry ref usable for `LocalImageExists`. Short names need cfg; full refs pass through.
// (Ported verbatim from the deleted former core ensure-image helper file; the remote-ref branch
// is gone — the caller (plugin_executor_reverse.go's BuilderStep ResolveImage closure) already
// routes a remote `@github.com/...` ref through the "remote-image-resolve" seam instead of
// calling this.)
//
// #55 coneB-br2: resolveImageRefPlugin's #8 cfg.ResolveBoxRef path (coneK1 #8, BANKED in
// candy/plugin-build/ensure.go) lifted host-side — byte-identical to the former
// deploykit.ResolveSpecBox → buildkit.ResolveBox path (namespace descent via SplitNamespaceRef,
// resolved.Name = leaf, resolved.Registry = img.Registry || nsCfg.Defaults.Registry, then
// ResolveShellImageRef). projectDir is retained in the signature for the host-internal caller
// but no longer needed for the resolve (cfg.ResolveBoxRef is pure config-nav — no build
// vocabulary fill).
func resolveImageRefForEnsure(image string, cfg *Config, projectDir string) (string, error) {
	_ = projectDir
	if image == "" {
		return "", fmt.Errorf("empty image")
	}
	if container.LooksLikeFullRef(image) {
		return image, nil
	}
	if cfg == nil {
		return "", fmt.Errorf("short name %q requires a project directory with charly.yml", image)
	}
	img, nsCfg, ok := cfg.ResolveBoxRef(image)
	if !ok {
		return "", fmt.Errorf("resolving %q via charly.yml: not found", image)
	}
	registry := img.Registry
	if registry == "" {
		registry = nsCfg.Defaults.Registry
	}
	return container.ResolveShellImageRef(registry, spec.LeafName(image), ""), nil
}
