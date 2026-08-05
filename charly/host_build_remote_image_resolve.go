package main

// host_build_remote_image_resolve.go — the "remote-image-resolve" HostBuild seam behind BOTH
// candy/plugin-build's build:ensure word (core-min wave 3) AND candy/plugin-box's dispatchBuild for
// `charly box build @ref` (P8b — after buildkit.DetectRemoteBuildRef detects the pivot sdk-side).
//
// K1 LOADER WAVE: the seam does ONLY the git clone/cache (EnsureRepoDownloaded — K1/B host floor,
// refs.go) + version resolution, returning the cached source dir + the box's short name. The former
// host-side box-RESOLVE this seam used to run (LoadConfig + the vocab fill + deploykit.ResolveSpecBox
// + ScanAllCandyWithConfig) — and the registry pull ref it produced — MOVED PLUGIN-SIDE: the calling
// plugin (candy/plugin-build's ensureRemoteRef) loads the cached repo's cfg via the K1 loader reverse
// legs + reads the box's registry/name itself (loaderkit.LoadUnified +
// cfg.BoxConfig(name).Registry || cfg.Defaults.Registry + kit.ResolveShellImageRef), shedding the
// deploykit import from charly core (the former backing file is DELETED; deploykit 15→14). The
// candy-map/Resolved/Config fields the old reply carried were dead output (ScanAllCandyWithConfig
// was wasted work — no caller read them). plugin-box's dispatchBuild uses only BoxName+CacheDir (it
// re-dispatches build:box with Dir=CacheDir), so it is unaffected by the pull-ref drop.

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/spec/refs"
	"github.com/opencharly/spec/spec"
)

const remoteImageResolveBuilderKind = "remote-image-resolve"

func hostBuildRemoteImageResolve(_ context.Context, req spec.RemoteImageResolveRequest, _ buildEngineContext) (spec.RemoteImageResolveReply, error) {
	parsed := spec.ParseRemoteRef(req.Ref)
	if parsed.RepoPath == "" || parsed.Name == "" {
		return spec.RemoteImageResolveReply{Error: fmt.Errorf("invalid remote image ref %q: expected @github.com/org/repo/image:version", req.Ref).Error()}, nil
	}
	version := parsed.Version
	if version == "" {
		repoURL := refs.RepoGitURL(parsed.RepoPath)
		tag, err := refs.GitLatestTag(repoURL)
		if err != nil {
			return spec.RemoteImageResolveReply{Error: fmt.Errorf("resolving latest version for %s: %w", parsed.RepoPath, err).Error()}, nil
		}
		version = tag
		fmt.Fprintf(os.Stderr, "Resolved @%s -> %s\n", parsed.RepoPath, version)
	}
	cachePath, err := requireProjectLoader().EnsureRepoDownloaded(hostInProcCtx(), parsed.RepoPath, version)
	if err != nil {
		return spec.RemoteImageResolveReply{Error: fmt.Errorf("downloading %s:%s: %w", parsed.RepoPath, version, err).Error()}, nil
	}
	return spec.RemoteImageResolveReply{
		CacheDir: cachePath,
		BoxName:  parsed.Name,
	}, nil
}

var _ = func() bool {
	registerHostBuilder(remoteImageResolveBuilderKind, typedHostBuilder(remoteImageResolveBuilderKind, hostBuildRemoteImageResolve))
	return true
}()
