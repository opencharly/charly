package main

// host_build_remote_image_resolve.go — the "remote-image-resolve" HostBuild seam behind BOTH
// candy/plugin-build's build:ensure word (core-min wave 3) AND candy/plugin-box's dispatchBuild for
// `charly box build @ref` (P8b — after buildkit.DetectRemoteBuildRef detects the pivot sdk-side): a
// thin wrapper around the EXISTING ResolveRemoteImage (remote_image.go, unchanged) that hands the plugin just what
// its ensure-image fallback needs — the registry pull ref + the cached source dir + the box's
// short name — dropping the *Config/*buildkit.ResolvedBox/candy-map fields that aren't wire-safe
// and aren't needed once the actual build re-dispatches through the existing build:box word with
// Dir set to CacheDir.

import (
	"context"

	"github.com/opencharly/sdk/spec"
)

const remoteImageResolveBuilderKind = "remote-image-resolve"

func hostBuildRemoteImageResolve(_ context.Context, req spec.RemoteImageResolveRequest, _ buildEngineContext) (spec.RemoteImageResolveReply, error) {
	rctx, err := ResolveRemoteImage(req.Ref, req.Tag)
	if err != nil {
		return spec.RemoteImageResolveReply{Error: err.Error()}, nil
	}
	return spec.RemoteImageResolveReply{
		ImageRef: rctx.ImageRef,
		CacheDir: rctx.CacheDir,
		BoxName:  rctx.BoxName,
	}, nil
}

var _ = func() bool {
	registerHostBuilder(remoteImageResolveBuilderKind, typedHostBuilder(remoteImageResolveBuilderKind, hostBuildRemoteImageResolve))
	return true
}()
