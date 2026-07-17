package main

import (
	"fmt"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
)

// DefaultProjectRepo, NormalizeRepoSpec — relocated (K1/W9): DefaultProjectRepo (a plain string
// constant, wide non-loader-cone consumer set) → sdk/spec as generic vocab; NormalizeRepoSpec (the
// --repo spec normalization mechanism, consumed only by already-loader-cone-coupled files) →
// sdk/loaderkit. charly/*.go call sites now reference spec.DefaultProjectRepo /
// loaderkit.NormalizeRepoSpec directly (ZERO-ALIASES — no alias reintroduced here).

// ResolveProjectRepo turns a --repo spec into a local cache path that can
// be passed to os.Chdir. Reuses the existing remote-candy cache machinery
// (RepoCacheDir, EnsureRepoDownloaded) so we don't have a second copy of
// "clone-and-cache".
func ResolveProjectRepo(spec string) (string, error) {
	if spec == "" {
		return "", fmt.Errorf("empty --repo spec")
	}
	repoPath, version := loaderkit.NormalizeRepoSpec(spec)
	if repoPath == "" {
		return "", fmt.Errorf("invalid --repo spec %q", spec)
	}
	if version == "" {
		branch, err := kit.GitDefaultBranch(kit.RepoGitURL(repoPath))
		if err != nil {
			return "", fmt.Errorf("resolving default branch for %s: %w", repoPath, err)
		}
		version = branch
	}
	return EnsureRepoDownloaded(repoPath, version)
}
