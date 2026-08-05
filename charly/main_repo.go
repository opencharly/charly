package main

import (
	"fmt"

	"github.com/opencharly/spec/refs"
	"github.com/opencharly/spec/spec"
)

// DefaultProjectRepo, NormalizeRepoSpec — both live in the dedicated spec module: DefaultProjectRepo
// (a plain string constant, wide non-loader-cone consumer set) as generic vocab; NormalizeRepoSpec
// (the --repo spec normalization helper, pure string logic) relocated there in the #55 2b Class A
// loader cascade. charly/*.go call sites reference spec.DefaultProjectRepo / spec.NormalizeRepoSpec
// directly (ZERO-ALIASES — no alias reintroduced here).

// ResolveProjectRepo turns a --repo spec into a local cache path that can
// be passed to os.Chdir. Reuses the existing remote-candy cache machinery
// (RepoCacheDir, EnsureRepoDownloaded) so we don't have a second copy of
// "clone-and-cache".
func ResolveProjectRepo(repoSpec string) (string, error) {
	if repoSpec == "" {
		return "", fmt.Errorf("empty --repo spec")
	}
	repoPath, version := spec.NormalizeRepoSpec(repoSpec)
	if repoPath == "" {
		return "", fmt.Errorf("invalid --repo spec %q", repoSpec)
	}
	if version == "" {
		branch, err := refs.GitDefaultBranch(refs.RepoGitURL(repoPath))
		if err != nil {
			return "", fmt.Errorf("resolving default branch for %s: %w", repoPath, err)
		}
		version = branch
	}
	return requireProjectLoader().EnsureRepoDownloaded(hostInProcCtx(), repoPath, version)
}
