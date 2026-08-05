package loader

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/refs"
	"github.com/opencharly/spec/spec"
)

// refs_seams.go — the loader plugin's remote-repo fetch entry points (K-wave 2 cone R1).
//
// These legs used to be spec.RefsCollectSeams built by charly core: core resolved each one against
// the provider registry and threaded the struct down into the relocated mechanism. That put core in
// the middle of every fetch for no reason the boundary law recognises — core was not DEFINING a
// mechanism there, only CALLING three providers and reading one env var on the loader's behalf, and
// the defines-vs-calls test says that is an R-item.
//
// A2 finished the move: the BUILDER itself now lives in sdk/loaderkit
// (loaderkit.RefsSeamsFromContext, refs_seams_executor.go) as the ONE copy, because
// candy/plugin-build needs the identical value to drive EnsureRepoDownloaded /
// CollectRemoteRefsOpts itself instead of round-tripping through charly's now-deleted
// `buildengine-ensure-repo` / `buildengine-collect-remote-refs` host legs. A second private copy
// here would be exactly the R3 duplicate this program removes.

// refsSeams builds the fetch legs for one call, delegating to the shared loaderkit builder.
func refsSeams(ctx context.Context) (spec.RefsCollectSeams, error) {
	return loaderkit.RefsSeamsFromContext(ctx)
}

// ResolveProjectRepo implements spec.ProjectLoader: it turns a `--repo` spec into a local cache path
// the caller can chdir into. Relocated from charly/main_repo.go (K-wave 2 cone R1) — its body was
// spec.NormalizeRepoSpec + a default-branch resolve + EnsureRepoDownloaded, i.e. pure spec vocabulary
// wrapped around the one fetch this file drives. Keeping it in the kernel meant the kernel held
// clone-and-cache logic for no reason the boundary law recognises.
func (*provider) ResolveProjectRepo(ctx context.Context, repoSpec string) (string, error) {
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
	seams, err := refsSeams(ctx)
	if err != nil {
		return "", err
	}
	return loaderkit.EnsureRepoDownloaded(repoPath, version, seams)
}
