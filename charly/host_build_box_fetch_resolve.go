package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// host_build_box_fetch_resolve.go — the "box-fetch-resolve" F10 host-builder behind
// candy/plugin-authoring's command:fetch/command:refresh (K3 build-tail tail, coneB-buildremnant):
// wraps the host-coupled repo resolver (ResolveProjectRepo → EnsureRepoDownloaded, refs.go — the
// CHARLY_REPO_OVERRIDE + registered refs-backend download + command:migrate auto-migration, none of
// which an sdk-only plugin can run itself) behind ONE generic seam, replacing the former hidden core
// `__box-fetch`/`__box-refresh` reentries (charly/box_fetch_reentry.go, DELETED). refs.go's
// EnsureRepoDownloaded is consumed AS-IS (unmodified) — coneC's P15 refs.go floor-reclassify needs it
// stable as this seam's callee.
const boxFetchResolveBuilderKind = "box-fetch-resolve"

// hostBuildBoxFetchResolve resolves req.Spec to its local cache path, force-removing the cache entry
// first when req.Refresh is set (the former BoxRefreshCmd body: normalize the spec, resolve a
// mutable-ref's default branch, remove any existing cache dir) so a stale cache re-clones.
func hostBuildBoxFetchResolve(_ context.Context, req spec.BoxFetchResolveRequest, _ buildEngineContext) (spec.BoxFetchResolveReply, error) {
	if req.Spec == "" {
		return spec.BoxFetchResolveReply{}, fmt.Errorf("box-fetch-resolve: empty spec")
	}
	if req.Refresh {
		repoPath, version := spec.NormalizeRepoSpec(req.Spec)
		if version == "" {
			branch, err := kit.GitDefaultBranch(kit.RepoGitURL(repoPath))
			if err != nil {
				return spec.BoxFetchResolveReply{}, fmt.Errorf("resolving default branch for %s: %w", repoPath, err)
			}
			version = branch
		}
		cachePath, err := kit.RepoCachePath(repoPath, version)
		if err != nil {
			return spec.BoxFetchResolveReply{}, err
		}
		if err := os.RemoveAll(cachePath); err != nil {
			return spec.BoxFetchResolveReply{}, fmt.Errorf("removing cache %s: %w", cachePath, err)
		}
	}
	path, err := ResolveProjectRepo(req.Spec)
	if err != nil {
		return spec.BoxFetchResolveReply{}, err
	}
	return spec.BoxFetchResolveReply{Path: path}, nil
}

var _ = func() bool {
	registerHostBuilder(boxFetchResolveBuilderKind, typedHostBuilder(boxFetchResolveBuilderKind, hostBuildBoxFetchResolve))
	return true
}()
