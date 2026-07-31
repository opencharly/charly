package main

// host_build_box_ref_resolve.go — the host-only box-ref resolve helper retained for the
// builder-venue reverse leg (#55 coneK1 #8: the "box-ref-resolve" HostBuild seam itself is
// DELETED — the build:ensure word now resolves the box ref PLUGIN-SIDE via the K1 loader reverse
// legs, shedding deploykit.ResolveSpecBox from that path; see candy/plugin-build/ensure.go's
// resolveImageRefPlugin / buildableShortNamePlugin).
//
// What STAYS here is resolveImageRefForEnsure: the project-coupled RESOLUTION a short/full image
// identifier needs (ResolveBox via deploykit.ResolveSpecBox — loader-cone, still core, tracked
// K1/K3 residue), kept for the ONE host-internal caller that cannot cross a process boundary:
// plugin_executor_reverse.go's injected ResolveImage closure (the BuilderStep host-step dispatch,
// plugin_executor_reverse.go:157), which closes over the in-process *Config the deploy walk
// already holds. The former buildableShortNameForEnsure + hostBuildBoxRefResolve host-builder are
// DELETED — the plugin owns the build-fallback resolve now (buildableShortNamePlugin).
//
// This file therefore KEEPS its deploykit import (resolveImageRefForEnsure →
// deploykit.ResolveSpecBox); the deploykit-COUNT drop for this file requires coneD to move
// resolveImageRefForEnsure's caller (plugin_executor_reverse.go:157) plugin-side — a coordination
// point, NOT #8 alone.

import (
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/spec"
)

// resolveImageRefForEnsure converts a user-authored image identifier into a fully-qualified
// registry ref usable for `LocalImageExists`. Short names need cfg; full refs pass through.
// (Ported verbatim from the deleted former core ensure-image helper file; the remote-ref branch
// is gone — the caller (plugin_executor_reverse.go's BuilderStep ResolveImage closure) already
// routes a remote `@github.com/...` ref through the "remote-image-resolve" seam instead of
// calling this.)
func resolveImageRefForEnsure(image string, cfg *Config, projectDir string) (string, error) {
	if image == "" {
		return "", fmt.Errorf("empty image")
	}
	if container.LooksLikeFullRef(image) {
		return image, nil
	}
	if cfg == nil {
		return "", fmt.Errorf("short name %q requires a project directory with charly.yml", image)
	}
	vopts, err := resolveVocabOpts(projectDir, spec.ResolveOpts{})
	if err != nil {
		return "", fmt.Errorf("resolving %q via charly.yml: %w", image, err)
	}
	resolved, err := deploykit.ResolveSpecBox(cfg, image, "", projectDir, vopts)
	if err != nil {
		return "", fmt.Errorf("resolving %q via charly.yml: %w", image, err)
	}
	return container.ResolveShellImageRef(resolved.Registry, resolved.Name, ""), nil
}
